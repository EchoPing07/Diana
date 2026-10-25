// Copyright (c) 2025-now SuInk.
// Licensed under the Limited Redistribution License in the repository root.

package assistant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// X 图片下载到宿主机临时目录后,路径必须换成桥能访问的共享 URL 再发出去。
// 桥运行在容器或另一台机器上时读不到宿主机路径,合并转发、暂存、散装会
// 挨个失败,重试耗尽后整条事件被丢弃——视频早有这层转换,图片曾经漏掉。
func TestOutgoingLocalImagePathsAreSharedBeforeSend(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	sharer := &recordingLocalMediaSharer{url: "https://share.example/img.jpg"}
	runtime.SetLocalMediaSharer(sharer)
	event := MessageEvent{Platform: PlatformOneBotV11, Kind: EventKindGroup, GroupID: "20005", UserID: "10001", MessageID: "m1"}
	local := writeLocalImage(t, "image.jpg")

	err := runtime.sendOutgoing(context.Background(), event, OutgoingMessage{
		ImageURLs: []string{local, "https://cdn.example/keep.png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sent := channel.sentSnapshot()
	if len(sent) != 1 {
		t.Fatalf("sent = %d", len(sent))
	}
	if sent[0].ImageURLs[0] != "https://share.example/img.jpg" {
		t.Fatalf("本地路径没有换成共享 URL:%#v", sent[0].ImageURLs)
	}
	if sent[0].ImageURLs[1] != "https://cdn.example/keep.png" {
		t.Fatalf("远程 URL 不该被改写:%#v", sent[0].ImageURLs)
	}
	if len(sharer.paths) != 1 || !strings.HasSuffix(sharer.paths[0], "image.jpg") {
		t.Fatalf("共享的应是本地文件路径:%#v", sharer.paths)
	}
}

// 没有配置共享器(桥与宿主同机部署)时保留原路径,行为不变。
func TestOutgoingLocalImagePathsKeptWithoutSharer(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{Platform: PlatformOneBotV11, Kind: EventKindGroup, GroupID: "20005", UserID: "10001", MessageID: "m1"}
	local := writeLocalImage(t, "image.jpg")

	if err := runtime.sendOutgoing(context.Background(), event, OutgoingMessage{
		ImageURLs: []string{local},
	}); err != nil {
		t.Fatal(err)
	}
	sent := channel.sentSnapshot()
	if len(sent) != 1 || sent[0].ImageURLs[0] != local {
		t.Fatalf("无共享器时应保留原路径:%#v", sent)
	}
}

// 只给走桥的 OneBot v11 换分享地址。Telegram、QQ 官方、微信、iMessage 都由后端
// 自己读字节上传，换成指向 127.0.0.1 的分享地址后，它们下载时走公网客户端、被
// SSRF 防护拒掉，图片一张也发不出去。
func TestOutgoingLocalImagePathsKeptForSameProcessChannels(t *testing.T) {
	for _, platform := range []string{PlatformTelegram, PlatformQQOfficial, PlatformWeixin, PlatformIMessage} {
		t.Run(platform, func(t *testing.T) {
			channel := &recordingChannel{}
			runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
			sharer := &recordingLocalMediaSharer{url: "http://127.0.0.1:18080/media/resolver/452ac2d2"}
			runtime.SetLocalMediaSharer(sharer)
			path := writeLocalImage(t, "sticker.gif")
			event := MessageEvent{Platform: platform, Kind: EventKindGroup, GroupID: "g1", UserID: "u1", MessageID: "m1"}

			if err := runtime.sendOutgoing(context.Background(), event, OutgoingMessage{ImageURLs: []string{path}}); err != nil {
				t.Fatal(err)
			}
			sent := channel.sentSnapshot()
			if len(sent) != 1 || sent[0].ImageURLs[0] != path {
				t.Fatalf("%s 应保留本地路径:%#v", platform, sent)
			}
			if len(sharer.paths) != 0 {
				t.Fatalf("%s 不应经过共享器:%#v", platform, sharer.paths)
			}
		})
	}
}

// writeLocalImage 在临时目录里造一张图片并返回绝对路径。文件要真写出来：出站链路会
// 读它算平台图片标识，路径不存在时每次都要等满等待窗口，用例会慢上几秒。
func writeLocalImage(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("gif"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
