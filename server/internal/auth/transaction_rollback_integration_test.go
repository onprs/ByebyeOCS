//go:build integration

package auth

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// pauseGate 控制客户端到服务器的字节转发；暂停后写出的回滚不会到达服务器。
type pauseGate struct {
	mutex  sync.Mutex
	paused bool
	resume chan struct{}
}

func newPauseGate() *pauseGate { return &pauseGate{resume: make(chan struct{})} }

func (gate *pauseGate) pause() {
	gate.mutex.Lock()
	defer gate.mutex.Unlock()
	if !gate.paused {
		gate.paused = true
		gate.resume = make(chan struct{})
	}
}

func (gate *pauseGate) unpause() {
	gate.mutex.Lock()
	defer gate.mutex.Unlock()
	if gate.paused {
		gate.paused = false
		close(gate.resume)
	}
}

func (gate *pauseGate) wait() {
	gate.mutex.Lock()
	paused, resume := gate.paused, gate.resume
	gate.mutex.Unlock()
	if paused {
		<-resume
	}
}

// stallProxy 是本地 PostgreSQL 的透明转发器，用于模拟“连接仍在但不产生应答”。
type stallProxy struct {
	listener net.Listener
	gate     *pauseGate
	host     string
	port     uint16
	network  string
	upstream string
	mutex    sync.Mutex
	conns    []net.Conn
	workers  sync.WaitGroup
}

func newStallProxy(t *testing.T, source *pgxpool.Config) *stallProxy {
	t.Helper()
	proxy := &stallProxy{gate: newPauseGate(), network: "tcp"}
	if strings.HasPrefix(source.ConnConfig.Host, "/") {
		proxy.network = "unix"
		proxy.upstream = filepath.Join(source.ConnConfig.Host,
			".s.PGSQL."+strconv.Itoa(int(source.ConnConfig.Port)))
		directory := t.TempDir()
		listener, err := net.Listen("unix", filepath.Join(directory, filepath.Base(proxy.upstream)))
		if err != nil {
			t.Fatalf("无法建立测试转发监听：%v", err)
		}
		proxy.listener = listener
		proxy.host = directory
		proxy.port = source.ConnConfig.Port
		go proxy.accept()
		return proxy
	}
	proxy.upstream = net.JoinHostPort(source.ConnConfig.Host,
		strconv.Itoa(int(source.ConnConfig.Port)))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法建立测试转发监听：%v", err)
	}
	proxy.listener = listener
	proxy.host = "127.0.0.1"
	proxy.port = uint16(listener.Addr().(*net.TCPAddr).Port)
	go proxy.accept()
	return proxy
}

func (proxy *stallProxy) accept() {
	for {
		client, err := proxy.listener.Accept()
		if err != nil {
			return
		}
		upstream, err := net.DialTimeout(proxy.network, proxy.upstream, 5*time.Second)
		if err != nil {
			_ = client.Close()
			continue
		}
		proxy.mutex.Lock()
		proxy.conns = append(proxy.conns, client, upstream)
		proxy.mutex.Unlock()
		proxy.workers.Add(2)
		go proxy.relay(upstream, client, true)
		go proxy.relay(client, upstream, false)
	}
}

// relay 单向转发；gated 方向在暂停期间不发送已读出的字节。
func (proxy *stallProxy) relay(destination io.WriteCloser, source io.ReadCloser, gated bool) {
	defer proxy.workers.Done()
	defer destination.Close()
	defer source.Close()
	buffer := make([]byte, 32*1024)
	for {
		n, err := source.Read(buffer)
		if n > 0 {
			if gated {
				proxy.gate.wait()
			}
			if _, writeErr := destination.Write(buffer[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (proxy *stallProxy) close() {
	proxy.gate.unpause()
	_ = proxy.listener.Close()
	proxy.mutex.Lock()
	for _, conn := range proxy.conns {
		_ = conn.Close()
	}
	proxy.mutex.Unlock()
	proxy.workers.Wait()
}

// 服务器停止应答时，inTx 的 defer 回滚必须仍能在有界时间内返回并释放连接池槽位；
// 未提交事务由服务器在连接断开时回滚，已提交数据与耐久性不受影响。
func TestTransactionRollbackIsBoundedWhenServerStopsResponding(t *testing.T) {
	direct := testsupport.NewPool(t)
	proxy := newStallProxy(t, direct.Config())
	defer proxy.close()

	proxied := direct.Config()
	proxied.MaxConns = 1
	proxied.MinConns = 0
	proxied.ConnConfig.Host = proxy.host
	proxied.ConnConfig.Port = proxy.port
	proxied.ConnConfig.Fallbacks = nil

	previous := txRollbackTimeout
	txRollbackTimeout = 200 * time.Millisecond
	defer func() { txRollbackTimeout = previous }()

	pool, err := pgxpool.NewWithConfig(context.Background(), proxied)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("经测试转发器连接数据库失败：%v", err)
	}
	service := &Service{pool: pool}

	uncommitted := web.NewID()
	type outcome struct {
		err     error
		elapsed time.Duration
	}
	results := make(chan outcome, 1)
	go func() {
		started := time.Now()
		err := service.inTx(ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO public.users (id, status) VALUES ($1, 'active')`,
				uncommitted); err != nil {
				return err
			}
			// 事务已写入但未提交；随后服务器不再应答回滚。
			proxy.gate.pause()
			return fault.New("service_unavailable", "模拟事务中断")
		})
		results <- outcome{err: err, elapsed: time.Since(started)}
	}()
	var finished outcome
	select {
	case finished = <-results:
	case <-time.After(5 * time.Second):
		proxy.gate.unpause()
		select {
		case <-results:
		case <-time.After(3 * time.Second):
		}
		t.Fatal("服务器停止应答时回滚无界等待")
	}
	proxy.gate.unpause()
	if code, _ := fault.Describe(finished.err); code != "service_unavailable" {
		t.Fatalf("中断事务返回错误码 %s", code)
	}
	if finished.elapsed > 2*time.Second {
		t.Fatalf("回滚耗时 %s，超过有界期限", finished.elapsed)
	}

	deadline := time.Now().Add(2 * time.Second)
	for pool.Stat().AcquiredConns() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("回滚失败后未释放连接池槽位")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var count int
	if err := direct.QueryRow(ctx, `SELECT count(*) FROM public.users WHERE id = $1`,
		uncommitted).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("中断事务的写入被提交")
	}
}
