package chaoxing

import (
	"context"
	"net/url"
	"testing"
)

func TestPlatformSessionClientNeverReusesAnotherSubjectsJar(t *testing.T) {
	server := testPlatformServer(t)
	defer server.Close()
	platform := testAdapter(t, server)
	first := testSession()
	ctx, cleanup, err := platform.withSessionClient(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	client, closeFirst, err := platform.requestClient(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFirst()
	second := testSession()
	second.Cookies = append([]Cookie(nil), first.Cookies...)
	second.Cookies[0].Value = "another-fixture-subject"
	other, closeOther, err := platform.requestClient(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	defer closeOther()
	if client == other || client.Jar == other.Jar {
		t.Fatal("复用了不同账号的HTTP会话")
	}
	target, _ := url.Parse(server.URL)
	for _, cookie := range other.Jar.Cookies(target) {
		if cookie.Name == "UID" && cookie.Value != "another-fixture-subject" {
			t.Fatal("新账号Cookie被旧账号覆盖")
		}
	}
	again, closeAgain, err := platform.requestClient(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAgain()
	if again != client {
		t.Fatal("同一次操作没有复用原账号会话")
	}
}
