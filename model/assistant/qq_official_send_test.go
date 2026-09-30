package assistant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type qqFakeAPI struct {
	mu       sync.Mutex
	messages []map[string]any
	// messagePosts 记录每次打到 /messages 的请求体（含被去重拒收的那次）。
	messagePosts []map[string]any
	// prepares 记录每次 /upload_prepare 的请求体，用来核对 file_type。
	prepares []map[string]any
	paths    []string
	put      []byte
	// dedup 让前 N 条 /messages 回 40054005，模拟平台去重。
	dedup  int
	server *httptest.Server
}

func newQQFakeAPI(t *testing.T) *qqFakeAPI {
	t.Helper()
	api := &qqFakeAPI{}
	mux := http.NewServeMux()
	mux.HandleFunc("/put/1", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		api.mu.Lock()
		api.put = body
		api.paths = append(api.paths, "PUT "+r.URL.Path)
		api.mu.Unlock()
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		api.mu.Lock()
		api.paths = append(api.paths, r.Method+" "+r.URL.Path)
		api.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/upload_prepare"):
			api.mu.Lock()
			api.prepares = append(api.prepares, body)
			api.mu.Unlock()
			size := body["file_size"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"upload_id": "up1", "block_size": size,
				"parts": []map[string]any{{"index": 1, "presigned_url": api.server.URL + "/put/1"}},
			})
		case strings.HasSuffix(r.URL.Path, "/upload_part_finish"):
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			_ = json.NewEncoder(w).Encode(map[string]any{"file_info": "FILEINFO", "ttl": 0})
		case strings.HasSuffix(r.URL.Path, "/messages"):
			api.mu.Lock()
			api.messagePosts = append(api.messagePosts, body)
			dedup := api.dedup > 0
			if dedup {
				api.dedup--
			} else {
				api.messages = append(api.messages, body)
			}
			n := len(api.messages)
			api.mu.Unlock()
			if dedup {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": "消息被去重，请检查请求msgseq", "code": 40054005})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "m" + string(rune('0'+n))})
		default:
			http.NotFound(w, r)
		}
	})
	api.server = httptest.NewServer(mux)
	t.Cleanup(api.server.Close)
	return api
}

func (a *qqFakeAPI) channel() *QQOfficialChannel {
	channel := NewQQOfficialChannel(QQOfficialConfig{AppID: "app", AppSecret: "secret"})
	channel.apiBaseOverride = a.server.URL
	channel.tokens = &platformTokenCache{fetch: func(context.Context) (string, time.Duration, error) {
		return "token", time.Hour, nil
	}}
	return channel
}

func TestQQOfficialSendsTextThenImageWithConsecutiveSeq(t *testing.T) {
	api := newQQFakeAPI(t)
	channel := api.channel()
	png := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	result, err := channel.SendWithResult(context.Background(), OutgoingMessage{
		GroupID: "G", Text: "看图", ImageURLs: []string{png}, ReplyMessageID: "in1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["message_id"] != "m1" {
		t.Fatalf("result = %#v", result)
	}
	if len(api.messages) != 2 {
		t.Fatalf("messages = %#v", api.messages)
	}
	text, image := api.messages[0], api.messages[1]
	if text["content"] != "看图" || text["msg_seq"].(float64) != 1 {
		t.Fatalf("text message = %#v", text)
	}
	media, _ := image["media"].(map[string]any)
	if image["msg_type"].(float64) != 7 || media["file_info"] != "FILEINFO" || image["msg_seq"].(float64) != 2 || image["msg_id"] != "in1" {
		t.Fatalf("image message = %#v", image)
	}
	if len(api.put) == 0 {
		t.Fatal("image bytes were never uploaded to the presigned URL")
	}
	want := []string{"POST /v2/groups/G/messages", "POST /v2/groups/G/upload_prepare", "PUT /put/1", "POST /v2/groups/G/upload_part_finish", "POST /v2/groups/G/files", "POST /v2/groups/G/messages"}
	if strings.Join(api.paths, "|") != strings.Join(want, "|") {
		t.Fatalf("request order = %v", api.paths)
	}
}

func TestQQOfficialProactiveMessageHasNoMsgSeq(t *testing.T) {
	api := newQQFakeAPI(t)
	if _, err := api.channel().SendWithResult(context.Background(), OutgoingMessage{GroupID: "G", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if _, has := api.messages[0]["msg_seq"]; has {
		t.Fatalf("proactive message carried msg_seq: %#v", api.messages[0])
	}
}

// 链接解析出视频后的真实形态：正文 + 封面图 + 本地 mp4。此前视频在这一步被静默
// 丢弃（只发文字和封面、不报错）；现在按 file_type=2 分片上传后用富媒体消息发出。
func TestQQOfficialSendsVideoAfterTextAndCover(t *testing.T) {
	api := newQQFakeAPI(t)
	channel := api.channel()
	dir := t.TempDir()
	video := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(video, append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, make([]byte, 64*1024)...), 0o600); err != nil {
		t.Fatal(err)
	}
	png := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	result, err := channel.SendWithResult(context.Background(), OutgoingMessage{
		GroupID:        "G",
		Text:           "[Bilibili] 标题",
		ImageURLs:      []string{png},
		VideoURLs:      []string{video},
		ReplyMessageID: "in1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["message_id"] != "m1" {
		t.Fatalf("result = %#v", result)
	}
	if len(api.messages) != 3 {
		t.Fatalf("messages = %#v, want text + cover + video", api.messages)
	}
	if api.messages[0]["content"] != "[Bilibili] 标题" || api.messages[0]["msg_type"].(float64) != 0 {
		t.Fatalf("text message = %#v", api.messages[0])
	}
	videoMsg := api.messages[2]
	media, _ := videoMsg["media"].(map[string]any)
	if videoMsg["msg_type"].(float64) != 7 || media["file_info"] != "FILEINFO" || videoMsg["msg_seq"].(float64) != 3 {
		t.Fatalf("video message = %#v", videoMsg)
	}
	if len(api.prepares) != 2 {
		t.Fatalf("upload_prepare calls = %#v, want one for the cover and one for the video", api.prepares)
	}
	if api.prepares[0]["file_type"].(float64) != 1 {
		t.Fatalf("cover prepare = %#v, want file_type 1", api.prepares[0])
	}
	if prepare := api.prepares[1]; prepare["file_type"].(float64) != 2 || prepare["file_name"] != "video.mp4" {
		t.Fatalf("video prepare = %#v, want file_type 2 named video.mp4", prepare)
	}
}

// 只发视频时此前不发任何请求、仍返回成功；现在会发出视频。
func TestQQOfficialSendsVideoOnlyMessage(t *testing.T) {
	api := newQQFakeAPI(t)
	channel := api.channel()
	video := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(video, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := channel.SendWithResult(context.Background(), OutgoingMessage{
		GroupID:   "G",
		VideoURLs: []string{video},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.messages) != 1 || api.messages[0]["msg_type"].(float64) != 7 {
		t.Fatalf("messages = %#v, want one rich-media message", api.messages)
	}
	if result == nil || result["message_id"] == "" {
		t.Fatalf("result = %#v, want the platform message id", result)
	}
}

// 音频按 file_type=3 上传，平台会转为语音消息。
func TestQQOfficialSendsAudioAsVoiceMessage(t *testing.T) {
	api := newQQFakeAPI(t)
	channel := api.channel()
	audio := filepath.Join(t.TempDir(), "song.mp3")
	if err := os.WriteFile(audio, make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := channel.SendWithResult(context.Background(), OutgoingMessage{
		GroupID:   "G",
		AudioURLs: []string{audio},
	}); err != nil {
		t.Fatal(err)
	}
	if len(api.messages) != 1 || api.messages[0]["msg_type"].(float64) != 7 {
		t.Fatalf("messages = %#v", api.messages)
	}
	if len(api.prepares) != 1 || api.prepares[0]["file_type"].(float64) != 3 || api.prepares[0]["file_name"] != "song.mp3" {
		t.Fatalf("prepares = %#v, want file_type 3 named song.mp3", api.prepares)
	}
}

// 40054005「消息被去重」换一个 msg_seq 重发一次可通过；通道内换号重试，
// 不把这种错误漏给上层当作普通失败。
func TestQQOfficialRetriesDedupRejectionWithNextSeq(t *testing.T) {
	api := newQQFakeAPI(t)
	api.dedup = 1
	channel := api.channel()
	if _, err := channel.SendWithResult(context.Background(), OutgoingMessage{
		GroupID:        "G",
		Text:           "看视频",
		ReplyMessageID: "in1",
	}); err != nil {
		t.Fatal(err)
	}
	if len(api.messages) != 1 {
		t.Fatalf("messages = %#v, want the retried message delivered", api.messages)
	}
	if len(api.messagePosts) != 2 {
		t.Fatalf("message posts = %#v, want one rejected and one retried", api.messagePosts)
	}
	if api.messagePosts[0]["msg_seq"].(float64) != 1 || api.messagePosts[1]["msg_seq"].(float64) != 2 {
		t.Fatalf("msg_seq values = %#v, want 1 then 2", api.messagePosts)
	}
}
