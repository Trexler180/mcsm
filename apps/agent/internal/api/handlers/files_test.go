package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"testing"
)

func multipartBody(t *testing.T, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("files", "upload.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, w.FormDataContentType()
}

func TestParseMultipartUploadRejectsRequestOverLimit(t *testing.T) {
	body, contentType := multipartBody(t, bytes.Repeat([]byte("x"), 256))
	req := httptest.NewRequest("POST", "/files/upload", body)
	req.Header.Set("Content-Type", contentType)

	err := parseMultipartUpload(httptest.NewRecorder(), req, int64(body.Len()-1))
	if err == nil {
		t.Fatal("expected oversized multipart body to be rejected")
	}
}

func TestParseMultipartUploadAcceptsRequestWithinLimit(t *testing.T) {
	body, contentType := multipartBody(t, []byte("hello"))
	req := httptest.NewRequest("POST", "/files/upload", body)
	req.Header.Set("Content-Type", contentType)

	if err := parseMultipartUpload(httptest.NewRecorder(), req, int64(body.Len())); err != nil {
		t.Fatalf("expected valid multipart body: %v", err)
	}
	defer req.MultipartForm.RemoveAll()
}

func TestMultipartFilesFitUsesAggregateFileSize(t *testing.T) {
	files := []*multipart.FileHeader{{Size: 6}, {Size: 5}}
	if multipartFilesFit(files, 10) {
		t.Fatal("expected aggregate file size over limit to be rejected")
	}
	if !multipartFilesFit(files, 11) {
		t.Fatal("expected aggregate file size at limit to be accepted")
	}
}
