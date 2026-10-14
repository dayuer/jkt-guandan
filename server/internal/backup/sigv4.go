package backup

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// 这里手写 AWS Signature Version 4（S3 用的签名），只覆盖备份需要的 PUT、GET、DELETE 和 ListObjectsV2，
// 避免为了四个请求引入整个 AWS SDK。正确性由 AWS 文档里的签名样例保证（sigv4_test.go）。

const (
	amzDateFormat = "20060102T150405Z"
	// emptySHA256 是空请求体的 sha256，GET 和 DELETE 用。
	emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

type credentials struct {
	region, accessKey, secretKey string
}

// sign 给 req 加上 x-amz-date、x-amz-content-sha256 和 Authorization 头。
// 签名覆盖 Host 和 req.Header 里的全部头（Transport 之后才加的 User-Agent 等不在其中）。
func (c credentials) sign(req *http.Request, payloadSHA256 string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format(amzDateFormat)
	day := amzDate[:8]
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadSHA256)

	headers := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		headers[strings.ToLower(k)] = strings.TrimSpace(strings.Join(v, ","))
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + headers[k] + "\n")
	}
	signedHeaders := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL.Query()),
		canonHeaders.String(),
		signedHeaders,
		payloadSHA256,
	}, "\n")

	scope := day + "/" + c.region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hexSHA256([]byte(canonical))

	key := hmacSHA256([]byte("AWS4"+c.secretKey), day)
	key = hmacSHA256(key, c.region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, toSign))

	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.accessKey+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

// canonicalURI 按 S3 的规则编码路径：每一段做 RFC 3986 编码，保留斜杠，不做二次编码。
func canonicalURI(u *url.URL) string {
	p := u.Path
	if p == "" {
		return "/"
	}
	return uriEncode(p, false)
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// uriEncode 是 SigV4 要求的编码：只保留 A-Z a-z 0-9 - _ . ~，其余按字节 %XX（大写）。
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			b.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
