package backup

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// 样例取自 AWS 文档「Signature Calculations for the Authorization Header: Transferring Payload
// in a Single Chunk」（AWS Signature Version 4，S3），凭据和签名都是文档公开的示例值。
var awsExample = credentials{
	region:    "us-east-1",
	accessKey: "AKIAIOSFODNN7EXAMPLE",
	secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
}

var awsExampleTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

func signature(t *testing.T, req *http.Request) string {
	t.Helper()
	auth := req.Header.Get("Authorization")
	i := strings.Index(auth, "Signature=")
	if i < 0 {
		t.Fatalf("Authorization 里没有签名：%s", auth)
	}
	return auth[i+len("Signature="):]
}

func TestSigV4GetObjectExample(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	awsExample.sign(req, emptySHA256, awsExampleTime)

	if got, want := signature(t, req), "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"; got != want {
		t.Fatalf("签名 %s，期望 %s", got, want)
	}
	if !strings.Contains(req.Header.Get("Authorization"),
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date") {
		t.Fatalf("SignedHeaders 不对：%s", req.Header.Get("Authorization"))
	}
}

func TestSigV4ListObjectsExample(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J", nil)
	awsExample.sign(req, emptySHA256, awsExampleTime)

	if got, want := signature(t, req), "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"; got != want {
		t.Fatalf("签名 %s，期望 %s", got, want)
	}
}

func TestURIEncode(t *testing.T) {
	if got := uriEncode("prod/guandan 2026+x~.db", false); got != "prod/guandan%202026%2Bx~.db" {
		t.Fatalf("路径编码 %s", got)
	}
	if got := uriEncode("a/b", true); got != "a%2Fb" {
		t.Fatalf("查询编码 %s", got)
	}
}
