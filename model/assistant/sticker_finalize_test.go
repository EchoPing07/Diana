// Copyright (c) 2025-now SuInk.
// Licensed under the Limited Redistribution License in the repository root.

package assistant

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/SuInk/diana/model/llm"
)

// stickerFinalizeLLMProvider 在收尾时按需填 sticker；sawField 记下收尾工具是否带了这个字段。
type stickerFinalizeLLMProvider struct {
	capturingLLMProvider
	sticker  string
	sawField bool
}

func (p *stickerFinalizeLLMProvider) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	response, err := p.capturingLLMProvider.Generate(ctx, req)
	if err != nil || response.Text != p.reply {
		return response, err
	}
	for _, tool := range req.Tools {
		if tool.Name != "agent_finalize" {
			continue
		}
		properties, _ := tool.Parameters["properties"].(map[string]any)
		_, p.sawField = properties[stickerFinalizeFieldName]
		arguments := map[string]any{"content": response.Text}
		if p.sticker != "" {
			arguments[stickerFinalizeFieldName] = p.sticker
		}
		response.ToolCalls = []llm.ToolCall{{ID: "finalize", Name: tool.Name, Arguments: arguments}}
		response.Text = ""
	}
	return response, nil
}

// 模型收尾时填了关键词：正文先发，紧跟一张命中的表情包；没填就只发正文。
func TestReplyFinalizeStickerFollowsText(t *testing.T) {
	withFastSendTiming(t)
	path := filepath.Join(t.TempDir(), "smug.gif")
	body := []byte("smug-sticker")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sticker := range []string{"得意 叉腰", ""} {
		channel := &recordingChannel{}
		provider := &stickerFinalizeLLMProvider{capturingLLMProvider: capturingLLMProvider{reply: "嘿嘿，被你发现了"}, sticker: sticker}
		rt := NewRuntime(BotConfig{AgentEnabled: true}.WithDefaults(), channel, NewDefaultPluginManager(), nil, nil, nil, func() (LLMProvider, error) { return provider, nil })
		event := MessageEvent{Kind: EventKindPrivate, UserID: "10001", MessageID: "tease", RawMessage: "你是不是偷吃了"}
		rt.SetMessageHistoryStore(&stickerHistoryStore{events: map[string][]MessageEvent{sessionKey(event): {{
			Kind: EventKindPrivate, UserID: "10001", MessageID: "s", Time: 1,
			Segments: []MessageSegment{{Type: "image", Data: map[string]string{"summary": "[得意]", "cached_file": path, imageContentSHA256Key: imageBytesSHA256(body)}}},
		}}}})
		if _, err := rt.replyTo(context.Background(), event, event.RawMessage); err != nil {
			t.Fatal(err)
		}
		if !provider.sawField {
			t.Fatal("agent_finalize did not offer the sticker field")
		}
		sent := channel.sentSnapshot()
		if sticker == "" {
			if len(sent) != 1 || len(sent[0].ImageURLs) != 0 {
				t.Fatalf("no keywords: sent = %#v", sent)
			}
			continue
		}
		if len(sent) != 2 || sent[0].Text == "" || len(sent[1].ImageURLs) != 1 || sent[1].ImageURLs[0] != path {
			t.Fatalf("with keywords: sent = %#v", sent)
		}
	}
}

// 自动配图只发关键词命中的；库里没有命中时宁可不发，也不拿随机补位的顶上。
func TestStickerSendBestMatchRequiresKeywordHit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cat.gif")
	body := []byte("cat")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	event := MessageEvent{Kind: EventKindGroup, GroupID: "g", UserID: "u", MessageID: "m"}
	channel := &recordingChannel{}
	rt := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	rt.SetMessageHistoryStore(&stickerHistoryStore{events: map[string][]MessageEvent{sessionKey(event): {{
		Kind: EventKindGroup, GroupID: "g", MessageID: "s", Time: 1,
		Segments: []MessageSegment{{Type: "image", Data: map[string]string{"summary": "[猫猫翻白眼]", "cached_file": path, imageContentSHA256Key: imageBytesSHA256(body)}}},
	}}}})
	tool := newDianaStickerTool(rt, event, nil)
	if sent, err := tool.sendBestMatch(context.Background(), "晚安 摸头"); err != nil || sent || len(channel.sentSnapshot()) != 0 {
		t.Fatalf("unmatched keywords sent=%v err=%v", sent, err)
	}
	if sent, err := tool.sendBestMatch(context.Background(), "翻白眼 无语"); err != nil || !sent || len(channel.sentSnapshot()) != 1 {
		t.Fatalf("matched keywords sent=%v err=%v", sent, err)
	}
	// 同一轮已经发过一张，单轮上限默认 1。
	if sent, _ := tool.sendBestMatch(context.Background(), "翻白眼"); sent || len(channel.sentSnapshot()) != 1 {
		t.Fatal("second sticker in the same turn was sent")
	}
}
