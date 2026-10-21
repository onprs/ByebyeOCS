// Package browser 实现默认关闭的可选 Chrome/HTML5 视频执行器。
//
// 该执行器只使用正常网页播放器、正常 play()/pause() 和平台页面脚本，
// 不发送进度上报、不伪造时长、不跳转、不合成播放/完成事件。平台确认仍由
// 独立 Platform verifier 完成。没有真实账号和目标平台 DOM 验证前，默认不启用。
package browser

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// cdpError 是 DevTools 返回的协议错误。
type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string { return fmt.Sprintf("CDP %d: %s", e.Code, e.Message) }

type cdpResponse struct {
	Result json.RawMessage
	Err    error
}

type cdpMessage struct {
	ID        int             `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

// cdpConn 通过 --remote-debugging-pipe 与 Chrome 通信。
// 使用管道而不是 TCP 端口，避免 CDP 暴露到网络。
type cdpConn struct {
	reader    *bufio.Reader
	writer    io.Writer
	writeMu   sync.Mutex
	mu        sync.Mutex
	nextID    int
	pending   map[int]chan cdpResponse
	closed    chan struct{}
	closeOnce sync.Once
	err       error
	maxBytes  int
}

func newCDPConn(reader io.Reader, writer io.Writer, maxBytes int) *cdpConn {
	if maxBytes <= 0 {
		maxBytes = 8 << 20
	}
	conn := &cdpConn{
		reader: bufio.NewReaderSize(reader, 64*1024), writer: writer,
		pending: map[int]chan cdpResponse{}, closed: make(chan struct{}), maxBytes: maxBytes,
	}
	go conn.readLoop()
	return conn
}

func (c *cdpConn) readLoop() {
	for {
		payload, err := c.readMessage()
		if err != nil {
			c.fail(err)
			return
		}
		var message cdpMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			c.fail(errors.New("CDP 消息无法解析"))
			return
		}
		if message.ID == 0 {
			if message.Method == "Inspector.targetCrashed" {
				c.fail(errors.New("浏览器目标崩溃"))
				return
			}
			continue
		}
		c.mu.Lock()
		waiter := c.pending[message.ID]
		delete(c.pending, message.ID)
		c.mu.Unlock()
		if waiter == nil {
			continue
		}
		if message.Error != nil {
			waiter <- cdpResponse{Err: message.Error}
		} else {
			waiter <- cdpResponse{Result: message.Result}
		}
	}
}

func (c *cdpConn) readMessage() ([]byte, error) {
	var buffer bytes.Buffer
	for {
		chunk, err := c.reader.ReadSlice(0)
		if len(chunk) > 0 && chunk[len(chunk)-1] == 0 {
			buffer.Write(chunk[:len(chunk)-1])
			return buffer.Bytes(), nil
		}
		buffer.Write(chunk)
		if buffer.Len() > c.maxBytes {
			return nil, errors.New("CDP 消息超过预算")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
	}
}

func (c *cdpConn) fail(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.err = err
		pending := c.pending
		c.pending = map[int]chan cdpResponse{}
		c.mu.Unlock()
		close(c.closed)
		for _, waiter := range pending {
			waiter <- cdpResponse{Err: errors.New("浏览器连接已关闭")}
		}
	})
}

func (c *cdpConn) call(ctx context.Context, method string, params any, sessionID string) (json.RawMessage, error) {
	if params == nil {
		params = map[string]any{}
	}
	rawParams, err := json.Marshal(params)
	if err != nil {
		return nil, errors.New("CDP 参数无法编码")
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return nil, err
	}
	c.nextID++
	id := c.nextID
	waiter := make(chan cdpResponse, 1)
	c.pending[id] = waiter
	c.mu.Unlock()

	payload, err := json.Marshal(cdpMessage{ID: id, Method: method, Params: rawParams, SessionID: sessionID})
	if err != nil {
		c.removePending(id)
		return nil, errors.New("CDP 命令无法编码")
	}
	c.writeMu.Lock()
	_, writeErr := c.writer.Write(append(payload, 0))
	c.writeMu.Unlock()
	if writeErr != nil {
		c.removePending(id)
		c.fail(writeErr)
		return nil, errors.New("CDP 命令写入失败")
	}
	select {
	case <-ctx.Done():
		c.removePending(id)
		return nil, ctx.Err()
	case <-c.closed:
		return nil, errors.New("浏览器连接已关闭")
	case response := <-waiter:
		if response.Err != nil {
			return nil, response.Err
		}
		return response.Result, nil
	}
}

func (c *cdpConn) removePending(id int) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *cdpConn) Close() {
	c.fail(errors.New("浏览器连接已关闭"))
}
