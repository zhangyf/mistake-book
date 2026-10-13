package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	cos "github.com/tencentyun/cos-go-sdk-v5"
	"github.com/zhangyf/objstore"
)

// COS 根前缀与索引键
const (
	rootPrefix = "exam-bank"
	indexKey   = rootPrefix + "/index.json"
)

// Storage 封装 objstore.Store，负责错题本的全部 COS 读写。
type Storage struct {
	store objstore.Store
}

// NewStorage 用环境变量或 ~/.workbuddy/cos-credentials 下的凭证创建 Storage。
func NewStorage() (*Storage, error) {
	cfg, err := loadCOSConfig()
	if err != nil {
		return nil, fmt.Errorf("加载 COS 凭证失败: %w", err)
	}
	store, err := objstore.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("创建 objstore 失败: %w", err)
	}
	return &Storage{store: store}, nil
}

// credDir 返回凭证目录：与 jrp / study-tracker 共用 ~/.workbuddy/cos-credentials。
// 解析顺序：MISTAKE_BOOK_COS_SKILL_DIR 环境变量 → ~/.workbuddy/cos-credentials。
func credDir() string {
	if dir := os.Getenv("MISTAKE_BOOK_COS_SKILL_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".workbuddy", "cos-credentials")
	}
	return filepath.Join(home, ".workbuddy", "cos-credentials")
}

// loadCOSConfig 依次尝试：环境变量 → 明文 .env → 加密 .env.enc。
func loadCOSConfig() (objstore.Config, error) {
	if id, key := os.Getenv("TENCENT_COS_SECRET_ID"), os.Getenv("TENCENT_COS_SECRET_KEY"); id != "" && key != "" {
		return objstore.Config{
			Provider:  objstore.ProviderCOS,
			Bucket:    os.Getenv("TENCENT_COS_BUCKET"),
			Region:    os.Getenv("TENCENT_COS_REGION"),
			SecretID:  id,
			SecretKey: key,
		}, nil
	}

	dir := credDir()
	if plain, err := os.ReadFile(filepath.Join(dir, ".env")); err == nil {
		vars := parseEnvFile(string(plain))
		if vars["TENCENT_COS_SECRET_ID"] != "" && vars["TENCENT_COS_SECRET_KEY"] != "" {
			return objstore.Config{
				Provider:  objstore.ProviderCOS,
				Bucket:    vars["TENCENT_COS_BUCKET"],
				Region:    vars["TENCENT_COS_REGION"],
				SecretID:  vars["TENCENT_COS_SECRET_ID"],
				SecretKey: vars["TENCENT_COS_SECRET_KEY"],
			}, nil
		}
	}

	encData, err := os.ReadFile(filepath.Join(dir, ".env.enc"))
	if err != nil {
		return objstore.Config{}, fmt.Errorf("环境变量未设置，且读不到 %s 下的 .env / .env.enc: %w", dir, err)
	}
	plaintext, err := decryptEnvFile(encData, dir)
	if err != nil {
		return objstore.Config{}, err
	}
	vars := parseEnvFile(plaintext)
	if vars["TENCENT_COS_SECRET_ID"] == "" || vars["TENCENT_COS_SECRET_KEY"] == "" {
		return objstore.Config{}, fmt.Errorf(".env.enc 里没有找到凭证")
	}
	return objstore.Config{
		Provider:  objstore.ProviderCOS,
		Bucket:    vars["TENCENT_COS_BUCKET"],
		Region:    vars["TENCENT_COS_REGION"],
		SecretID:  vars["TENCENT_COS_SECRET_ID"],
		SecretKey: vars["TENCENT_COS_SECRET_KEY"],
	}, nil
}

// deriveEnvKey 与 cos_node.mjs / jrp 保持一致：SHA-256(hostname:username:skillDir)
func deriveEnvKey(skillDir string) []byte {
	hostname, _ := os.Hostname()
	username := os.Getenv("USERNAME")
	if username == "" {
		username = os.Getenv("USER")
	}
	if username == "" {
		if u, err := user.Current(); err == nil {
			username = u.Username
		}
	}
	seed := fmt.Sprintf("%s:%s:%s", hostname, username, skillDir)
	key := sha256.Sum256([]byte(seed))
	return key[:]
}

// decryptEnvFile 解密格式：iv(12) + authTag(16) + ciphertext
func decryptEnvFile(encData []byte, skillDir string) (string, error) {
	if len(encData) < 28 {
		return "", fmt.Errorf("加密数据过短")
	}
	key := deriveEnvKey(skillDir)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	iv, authTag, ciphertext := encData[:12], encData[12:28], encData[28:]
	plaintext, err := gcm.Open(nil, iv, append(ciphertext, authTag...), nil)
	if err != nil {
		return "", fmt.Errorf("解密失败（机器/用户不匹配？）: %w", err)
	}
	return string(plaintext), nil
}

func parseEnvFile(content string) map[string]string {
	vars := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx == -1 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		if len(v) >= 2 && ((strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"")) ||
			(strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'"))) {
			v = v[1 : len(v)-1]
		}
		vars[k] = v
	}
	return vars
}

// ============================================================================
// 索引
// ============================================================================

// Index 是 exam-bank/index.json 的结构。
type Index struct {
	Version int                      `json:"version"`
	Items   []Entry                  `json:"items"`
	Names   map[string]string        `json:"names,omitempty"` // 科目 → 错题本显示名（老师自己起的，空 = 用科目名）
	Imports map[string]ImportReceipt `json:"imports,omitempty"`
}

// ImportReceipt 与条目在同一次索引写入中持久化；编辑、删除题目不删除回执。
type ImportReceipt struct {
	PayloadHash string   `json:"payload_sha256"`
	IDs         []string `json:"ids"`
}

// DisplayName 取某科目错题本的显示名：老师起过就用他的，否则用科目名。
func (idx *Index) DisplayName(subject string) string {
	if n := strings.TrimSpace(idx.Names[subject]); n != "" {
		return n
	}
	return subject
}

// Entry 是一道题的索引条目。
type Entry struct {
	ID        string   `json:"id"`
	Subject   string   `json:"subject"`
	Exam      string   `json:"exam"`
	Date      string   `json:"date"`
	Photo     string   `json:"photo"`
	Qno       string   `json:"qno"`
	Qtype     string   `json:"qtype,omitempty"`
	Result    string   `json:"result,omitempty"`
	Source    string   `json:"source,omitempty"`   // 错题出处：校内作业/课外班作业/校内考试/自我复习
	Analysis  string   `json:"analysis,omitempty"` // 错因要点（网页可改；非空时覆盖 md 里解析出的「错因/要点」）
	Tags      []string `json:"tags,omitempty"`
	MdKey     string   `json:"md_key,omitempty"`
	ImageKey  string   `json:"image_key,omitempty"`
	Note      string   `json:"note,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

// objectNotFound 只接受 COS 明确的对象缺失，桶缺失、权限和网络错误均不能当空库。
// objstore v0.17.0 没有 IsNotFound，GetAll 原样返回现有 COS SDK 的错误。
func objectNotFound(err error) bool {
	var ce *cos.ErrorResponse
	return errors.As(err, &ce) && ce != nil && ce.Response != nil &&
		ce.Response.StatusCode == http.StatusNotFound && ce.Code == "NoSuchKey"
}

// GetIndex 下载索引；仅对象确实不存在时返回空索引。
func (s *Storage) GetIndex(ctx context.Context) (*Index, error) {
	data, err := s.store.GetAll(ctx, indexKey)
	if err != nil {
		if objectNotFound(err) {
			return &Index{Version: 1, Items: []Entry{}, Names: map[string]string{}}, nil
		}
		return nil, errors.New("读取索引失败，已停止写入")
	}
	var raw map[string]json.RawMessage
	if err := decodeImportJSON(data, &raw); err != nil || raw == nil {
		return nil, errors.New("索引内容无效，已停止写入")
	}
	items := bytes.TrimSpace(raw["items"])
	if len(items) == 0 || items[0] != '[' {
		return nil, errors.New("索引缺少有效 items 数组，已停止写入")
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, errors.New("解析索引失败，已停止写入")
	}
	if idx.Version == 0 {
		idx.Version = 1
	}
	if idx.Names == nil {
		idx.Names = map[string]string{}
	}
	return &idx, nil
}

// PutIndex 上传索引。
func (s *Storage) PutIndex(ctx context.Context, idx *Index) error {
	if idx == nil {
		return errors.New("不能写入空索引")
	}
	out := *idx
	if out.Items == nil {
		out.Items = []Entry{}
	}
	data, err := json.MarshalIndent(&out, "", "  ")
	if err != nil {
		return err
	}
	return s.store.PutObject(ctx, indexKey, data)
}

// MergeEntries 按 id 合并条目，返回新增/更新的 id。
func (idx *Index) MergeEntries(items []Entry) (added, updated []string) {
	pos := make(map[string]int, len(idx.Items))
	for i, it := range idx.Items {
		if it.ID != "" {
			pos[it.ID] = i
		}
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	for _, it := range items {
		it.UpdatedAt = now
		if i, ok := pos[it.ID]; ok {
			merged := mergeEntry(idx.Items[i], it)
			idx.Items[i] = merged
			updated = append(updated, it.ID)
		} else {
			idx.Items = append(idx.Items, it)
			pos[it.ID] = len(idx.Items) - 1
			added = append(added, it.ID)
		}
	}
	sort.SliceStable(idx.Items, func(a, b int) bool {
		if idx.Items[a].Date != idx.Items[b].Date {
			return idx.Items[a].Date < idx.Items[b].Date
		}
		return idx.Items[a].ID < idx.Items[b].ID
	})
	return added, updated
}

// mergeEntry 用新条目覆盖旧字段（空值不覆盖），标签取并集。
func mergeEntry(old, neu Entry) Entry {
	out := old
	if neu.Subject != "" {
		out.Subject = neu.Subject
	}
	if neu.Exam != "" {
		out.Exam = neu.Exam
	}
	if neu.Date != "" {
		out.Date = neu.Date
	}
	if neu.Photo != "" {
		out.Photo = neu.Photo
	}
	if neu.Qno != "" {
		out.Qno = neu.Qno
	}
	if neu.Qtype != "" {
		out.Qtype = neu.Qtype
	}
	if neu.Result != "" {
		out.Result = neu.Result
	}
	if neu.Source != "" {
		out.Source = neu.Source
	}
	if neu.Analysis != "" {
		out.Analysis = neu.Analysis
	}
	if neu.MdKey != "" {
		out.MdKey = neu.MdKey
	}
	if neu.ImageKey != "" {
		out.ImageKey = neu.ImageKey
	}
	if neu.Note != "" {
		out.Note = neu.Note
	}
	out.Tags = unionTags(old.Tags, neu.Tags)
	out.UpdatedAt = neu.UpdatedAt
	return out
}

func unionTags(a, b []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, t := range append(append([]string{}, a...), b...) {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// ============================================================================
// 对象读写
// ============================================================================

// PutFile 上传本地文件。
func (s *Storage) PutFile(ctx context.Context, key, localPath string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("读取本地文件失败 %s: %w", localPath, err)
	}
	return s.store.PutObject(ctx, key, data)
}

// PutBytes 直接上传内存数据，导入路径不创建本地文件。
func (s *Storage) PutBytes(ctx context.Context, key string, data []byte) error {
	return s.store.PutObject(ctx, key, data)
}

// GetAll 下载对象内容。
func (s *Storage) GetAll(ctx context.Context, key string) ([]byte, error) {
	return s.store.GetAll(ctx, key)
}

// Presign 生成下载用预签名 URL。
func (s *Storage) Presign(ctx context.Context, key string, expires time.Duration) (string, error) {
	return s.store.PresignGetObject(ctx, key, expires)
}
