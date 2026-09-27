// Copyright (c) 2025-now SuInk.
// Licensed under the Limited Redistribution License in the repository root.

package assistant

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/SuInk/diana/model/agent"
)

// 让模型收尾时顺手填几个关键词，比让它先 tools_load sticker、再 search、再 send
// 容易得多：线上回放里，只改说明和提示词，模型在适合发表情包的场合一次都没去加载
// 工具（gpt-6-sol 0/180）。sticker 工具保留给「明确要表情包」这种场合。
const (
	stickerFinalizeFieldName = "sticker"
	// stickerOrderFieldName 让模型按真人习惯决定表情包和文字谁先发：第一反应先甩图，收尾点题后甩图。
	stickerOrderFieldName = "sticker_order"
	stickerOrderBefore    = "before"
)

func stickerFinalizeField() agent.FinalizeField {
	return agent.FinalizeField{
		Name: stickerFinalizeFieldName,
		Description: "想配一张表情包时，填 2 到 6 个空格分隔的短关键词（情绪、动作、场景和同义说法），例如“得意 叉腰”“晚安 摸头”。" +
			"有正文时和正文一起发，先后看 sticker_order；只想回一张图时填 silent=true、content 留空。挑不到合适的就不发，也不会补文字。配不配由你按当下的聊天自己判断。",
	}
}

func stickerOrderField() agent.FinalizeField {
	return agent.FinalizeField{
		Name:        stickerOrderFieldName,
		Description: "填了 sticker 又有正文时，表情包和文字谁先发：before 先甩图再说话，after 或留空先说完再甩图。按真人聊天的习惯自己判断。",
	}
}

type finalizeStickerKey struct{}

// finalizeSticker 把收尾时填的关键词从 generateReply 带到发送之后。只有主回复路径会
// 在 ctx 里放它；其他调用 generateReply 的路径（事件触发、后台任务）拿不到就不配图。
type finalizeSticker struct {
	mu     sync.Mutex
	query  string
	before bool
}

func withFinalizeSticker(ctx context.Context) (context.Context, *finalizeSticker) {
	holder := &finalizeSticker{}
	return context.WithValue(ctx, finalizeStickerKey{}, holder), holder
}

func finalizeStickerFromContext(ctx context.Context) *finalizeSticker {
	holder, _ := ctx.Value(finalizeStickerKey{}).(*finalizeSticker)
	return holder
}

func (f *finalizeSticker) set(query, order string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.query = strings.TrimSpace(query)
	f.before = strings.EqualFold(strings.TrimSpace(order), stickerOrderBefore)
	f.mu.Unlock()
}

// take 取出关键词和「是否先于文字发」并清空，同一轮只配一次。
func (f *finalizeSticker) take() (string, bool) {
	if f == nil {
		return "", false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	query, before := f.query, f.before
	f.query, f.before = "", false
	return query, before
}

// sendFinalizeSticker 在正文发出后按关键词配一张表情包。配不上、到了上限或发送失败都
// 只记日志：正文已经发出去了，表情包是锦上添花。
func (r *Runtime) sendFinalizeSticker(ctx context.Context, event MessageEvent, query string) {
	if strings.TrimSpace(query) == "" {
		return
	}
	_, settings, enabled := r.pluginWithSettingsForEvent(stickerPluginID, event)
	if !enabled {
		return
	}
	if _, err := newDianaStickerTool(r, event, settings).sendBestMatch(ctx, query); err != nil {
		log.Printf("diana finalize sticker failed: message_id=%s err=%v", event.MessageID, err)
	}
}
