package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Object 是异地备份里的一个对象（一份加密快照）。
type Object struct {
	Name     string // 不含前缀的对象名，如 guandan-20261013T160000Z.db.age
	Size     int64
	Modified time.Time
}

// Target 是异地备份的存储位置（§13.3）。
type Target interface {
	Put(ctx context.Context, name, localPath string) error
	Get(ctx context.Context, name string, w io.Writer) error
	List(ctx context.Context) ([]Object, error)
	Delete(ctx context.Context, name string) error
	String() string // 日志里用，不含凭据
}

// TargetConfig 是 NewTarget 需要的配置，来自 config.Config。
type TargetConfig struct {
	URL       string
	Region    string
	AccessKey string
	SecretKey string
}

// NewTarget 按 URL 的 scheme 选择实现：
//   - https://<endpoint>/<bucket>[/<前缀>]：S3 兼容存储，path-style 寻址（AWS、R2、B2、MinIO 都支持）；
//   - file:///<目录>：本机目录（挂载的网络盘，或本机演练用）。
func NewTarget(cfg TargetConfig) (Target, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("解析备份地址: %w", err)
	}
	switch u.Scheme {
	case "file":
		return &fileTarget{dir: u.Path}, nil
	case "https", "http":
		parts := strings.SplitN(strings.Trim(u.Path, "/"), "/", 2)
		if parts[0] == "" {
			return nil, fmt.Errorf("备份地址 %s 缺少 bucket", cfg.URL)
		}
		t := &s3Target{
			endpoint: u.Scheme + "://" + u.Host,
			bucket:   parts[0],
			creds:    credentials{region: cfg.Region, accessKey: cfg.AccessKey, secretKey: cfg.SecretKey},
			client:   &http.Client{Timeout: 5 * time.Minute}, // §12.2：备份上传 5 分钟
			now:      time.Now,
		}
		if len(parts) == 2 && strings.Trim(parts[1], "/") != "" {
			t.prefix = strings.Trim(parts[1], "/") + "/"
		}
		return t, nil
	default:
		return nil, fmt.Errorf("不支持的备份地址 %s", cfg.URL)
	}
}

// ---- file:// ----

type fileTarget struct{ dir string }

func (t *fileTarget) String() string { return "file://" + t.dir }

func (t *fileTarget) Put(_ context.Context, name, localPath string) error {
	if err := os.MkdirAll(t.dir, 0o700); err != nil {
		return err
	}
	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(t.dir, ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(t.dir, name))
}

func (t *fileTarget) Get(_ context.Context, name string, w io.Writer) error {
	if name != filepath.Base(name) {
		return fmt.Errorf("对象名 %q 不合法", name)
	}
	f, err := os.Open(filepath.Join(t.dir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func (t *fileTarget) List(context.Context) ([]Object, error) {
	entries, err := os.ReadDir(t.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Object
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, Object{Name: e.Name(), Size: info.Size(), Modified: info.ModTime()})
	}
	sortObjects(out)
	return out, nil
}

func (t *fileTarget) Delete(_ context.Context, name string) error {
	if name != filepath.Base(name) {
		return fmt.Errorf("对象名 %q 不合法", name)
	}
	return os.Remove(filepath.Join(t.dir, name))
}

// ---- S3 ----

type s3Target struct {
	endpoint string // https://host
	bucket   string
	prefix   string // 空，或以 / 结尾
	creds    credentials
	client   *http.Client
	now      func() time.Time
}

func (t *s3Target) String() string { return t.endpoint + "/" + t.bucket + "/" + t.prefix }

func (t *s3Target) objectURL(name string) string {
	return t.endpoint + "/" + t.bucket + "/" + t.prefix + name
}

func (t *s3Target) do(req *http.Request, payloadSHA256 string) (*http.Response, error) {
	t.creds.sign(req, payloadSHA256, t.now())
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		return nil, fmt.Errorf("%s %s：HTTP %d %s", req.Method, req.URL.Path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

func (t *s3Target) Put(ctx context.Context, name, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, t.objectURL(name), f)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := t.do(req, hex.EncodeToString(h.Sum(nil)))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (t *s3Target) Get(ctx context.Context, name string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.objectURL(name), nil)
	if err != nil {
		return err
	}
	resp, err := t.do(req, emptySHA256)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(w, resp.Body)
	return err
}

func (t *s3Target) Delete(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.objectURL(name), nil)
	if err != nil {
		return err
	}
	resp, err := t.do(req, emptySHA256)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

type listResult struct {
	Contents []struct {
		Key          string    `xml:"Key"`
		Size         int64     `xml:"Size"`
		LastModified time.Time `xml:"LastModified"`
	} `xml:"Contents"`
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
}

func (t *s3Target) List(ctx context.Context) ([]Object, error) {
	var out []Object
	token := ""
	for page := 0; page < 100; page++ {
		q := url.Values{"list-type": {"2"}}
		if t.prefix != "" {
			q.Set("prefix", t.prefix)
		}
		if token != "" {
			q.Set("continuation-token", token)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			t.endpoint+"/"+t.bucket+"?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := t.do(req, emptySHA256)
		if err != nil {
			return nil, err
		}
		var lr listResult
		err = xml.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&lr)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("解析对象列表: %w", err)
		}
		for _, c := range lr.Contents {
			name := strings.TrimPrefix(c.Key, t.prefix)
			if name == "" || strings.Contains(name, "/") {
				continue // 前缀下更深一层的对象不是我们的
			}
			out = append(out, Object{Name: name, Size: c.Size, Modified: c.LastModified})
		}
		if !lr.IsTruncated {
			sortObjects(out)
			return out, nil
		}
		token = lr.NextContinuationToken
	}
	return nil, errors.New("对象列表分页过多")
}

func sortObjects(objs []Object) {
	sort.Slice(objs, func(i, j int) bool { return objs[i].Name < objs[j].Name })
}
