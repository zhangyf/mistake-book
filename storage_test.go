package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	cos "github.com/tencentyun/cos-go-sdk-v5"
	"github.com/zhangyf/objstore"
)

// 未实现的方法保持 nil，误调用真实存储路径会立即失败，而不是访问网络。
type fakeImportStore struct {
	objstore.Store
	mu       sync.Mutex
	objects  map[string][]byte
	reads    map[string]int
	writes   []string
	onGet    func(*fakeImportStore, string) error
	onPut    func(*fakeImportStore, string, []byte) error
	afterPut func(*fakeImportStore, string)
}

func newFakeImportStore() *fakeImportStore {
	return &fakeImportStore{objects: map[string][]byte{}, reads: map[string]int{}}
}
func missingImportObject() error {
	return &cos.ErrorResponse{Code: "NoSuchKey", Response: &http.Response{
		StatusCode: 404, Request: httptest.NewRequest("GET", "/object", nil), Header: http.Header{},
	}}
}
func (f *fakeImportStore) GetAll(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads[key]++
	if f.onGet != nil {
		if err := f.onGet(f, key); err != nil {
			return nil, err
		}
	}
	data, ok := f.objects[key]
	if !ok {
		return nil, missingImportObject()
	}
	return bytes.Clone(data), nil
}
func (f *fakeImportStore) PutObject(_ context.Context, key string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, key)
	if f.onPut != nil {
		if err := f.onPut(f, key, data); err != nil {
			return err
		}
	}
	f.objects[key] = bytes.Clone(data)
	if f.afterPut != nil {
		f.afterPut(f, key)
	}
	return nil
}
func (f *fakeImportStore) seedIndex(idx Index) {
	if idx.Items == nil {
		idx.Items = []Entry{}
	}
	f.objects[indexKey], _ = json.Marshal(idx)
}
func (f *fakeImportStore) index(t *testing.T) Index {
	t.Helper()
	var idx Index
	if err := json.Unmarshal(f.objects[indexKey], &idx); err != nil {
		t.Fatal(err)
	}
	return idx
}
func (f *fakeImportStore) indexWrites() int {
	n := 0
	for _, key := range f.writes {
		if key == indexKey {
			n++
		}
	}
	return n
}

func TestStorageGetIndexFailsClosed(t *testing.T) {
	cases := []struct {
		name, data string
		readError  error
		ok         bool
	}{
		{"缺失", "", missingImportObject(), true},
		{"包装缺失", "", fmt.Errorf("包装: %w", missingImportObject()), true},
		{"权限错误", "", errors.New("denied secret"), false},
		{"网络错误", "", context.DeadlineExceeded, false},
		{"伪404文本", "", errors.New("404 NoSuchKey"), false},
		{"桶缺失", "", &cos.ErrorResponse{Code: "NoSuchBucket", Response: &http.Response{StatusCode: 404}}, false},
		{"状态不符", "", &cos.ErrorResponse{Code: "NoSuchKey", Response: &http.Response{StatusCode: 403}}, false},
		{"空文件", "", nil, false},
		{"空白", "  \n", nil, false},
		{"null", "null", nil, false},
		{"缺少items", `{"version":1}`, nil, false},
		{"items为null", `{"items":null}`, nil, false},
		{"items非数组", `{"items":{}}`, nil, false},
		{"数组根", `[]`, nil, false},
		{"破损", `{"items":[`, nil, false},
		{"重复items", `{"items":[{"id":"old"}],"items":[]}`, nil, false},
		{"重复回执", `{"items":[],"imports":{"id":{"payload_sha256":"a"},"id":{"payload_sha256":"b"}}}`, nil, false},
		{"有效空库", `{"items":[]}`, nil, true},
		{"有效条目", `{"version":1,"items":[{"id":"old"}]}`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeImportStore()
			f.objects[indexKey] = []byte(tc.data)
			if tc.readError != nil {
				f.onGet = func(*fakeImportStore, string) error { return tc.readError }
			}
			idx, err := (&Storage{store: f}).GetIndex(context.Background())
			if (err == nil) != tc.ok {
				t.Fatalf("成功=%v, 错误=%v", err == nil, err)
			}
			if tc.ok && (idx == nil || idx.Items == nil || idx.Names == nil || idx.Version != 1) {
				t.Fatalf("索引默认值无效: %+v", idx)
			}
			if len(f.writes) != 0 {
				t.Fatal("读取不应写入")
			}
		})
	}
}

func TestStoragePutIndexAndBytes(t *testing.T) {
	f := newFakeImportStore()
	st := &Storage{store: f}
	idx := &Index{Version: 1, Imports: map[string]ImportReceipt{"id": {PayloadHash: "hash", IDs: []string{"q1"}}}}
	if err := st.PutIndex(context.Background(), idx); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(f.objects[indexKey], []byte(`"items": null`)) {
		t.Fatal("不能写入 null items")
	}
	got, err := st.GetIndex(context.Background())
	if err != nil || got.Imports["id"].PayloadHash != "hash" {
		t.Fatalf("回执未保留: %v", err)
	}
	if err := st.PutBytes(context.Background(), "memory", []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.objects["memory"], []byte{1, 2, 3}) {
		t.Fatal("内存上传内容不同")
	}
	if err := st.PutIndex(context.Background(), nil); err == nil {
		t.Fatal("不能写入 nil 索引")
	}
}
