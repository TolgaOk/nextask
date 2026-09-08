// Package storagetest provides a local S3 protocol fixture for integration tests.
package storagetest

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Object struct {
	Data   []byte
	Digest string
}
type Server struct {
	*httptest.Server
	mu                       sync.Mutex
	objects                  map[string]Object
	puts, attempts, failures int
	delay                    time.Duration
}

func New() *Server {
	s := &Server{objects: make(map[string]Object)}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}
func (s *Server) FailPuts(n int)                { s.mu.Lock(); defer s.mu.Unlock(); s.failures = n }
func (s *Server) DelayPuts(delay time.Duration) { s.mu.Lock(); defer s.mu.Unlock(); s.delay = delay }
func (s *Server) Counts() (int, int)            { s.mu.Lock(); defer s.mu.Unlock(); return s.puts, s.attempts }
func (s *Server) Object(key string) (Object, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[key]
	o.Data = bytes.Clone(o.Data)
	return o, ok
}
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=test-access/") {
		s3Error(w, http.StatusForbidden, "AccessDenied")
		return
	}
	if r.Method == "GET" && r.URL.Query().Has("location") {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">fsn1</LocationConstraint>`)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/")
	if r.Method == "GET" && r.URL.Query().Get("list-type") == "2" {
		s.list(w, r, strings.TrimSuffix(key, "/"))
		return
	}
	switch r.Method {
	case "GET":
		o, ok := s.Object(key)
		if !ok {
			s3Error(w, 404, "NoSuchKey")
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(o.Data)))
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, md5.Sum(o.Data)))
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("X-Amz-Meta-Nextask-Sha256", o.Digest)
		w.Write(o.Data)

	case "HEAD":
		o, ok := s.Object(key)
		if !ok {
			s3Error(w, 404, "NoSuchKey")
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(o.Data)))
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, md5.Sum(o.Data)))
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("X-Amz-Meta-Nextask-Sha256", o.Digest)
		w.WriteHeader(200)
	case "PUT":
		s.mu.Lock()
		s.attempts++
		fail := s.failures > 0
		if fail {
			s.failures--
		}
		delay := s.delay
		s.mu.Unlock()
		if fail {
			s3Error(w, 500, "InternalError")
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(delay):
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			s3Error(w, 400, "IncompleteBody")
			return
		}
		if strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") {
			body, err = decodeChunks(body)
			if err != nil {
				s3Error(w, 400, "InvalidRequest")
				return
			}
		}
		if size := r.Header.Get("X-Amz-Decoded-Content-Length"); size != "" && size != strconv.Itoa(len(body)) {
			s3Error(w, 400, "IncompleteBody")
			return
		}
		s.mu.Lock()
		s.objects[key] = Object{body, r.Header.Get("X-Amz-Meta-Nextask-Sha256")}
		s.puts++
		s.mu.Unlock()
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, md5.Sum(body)))
		w.WriteHeader(200)
	default:
		s3Error(w, 405, "MethodNotAllowed")
	}
}
func s3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<Error><Code>%s</Code><Message>test storage error</Message></Error>", code)
}
func decodeChunks(raw []byte) ([]byte, error) {
	reader := bufio.NewReader(bytes.NewReader(raw))
	var out bytes.Buffer
	for {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		sizeText, _, _ := strings.Cut(strings.TrimSpace(header), ";")
		size, err := strconv.ParseInt(sizeText, 16, 64)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			return out.Bytes(), nil
		}
		if _, err := io.CopyN(&out, reader, size); err != nil {
			return nil, err
		}
		if _, err := reader.ReadString('\n'); err != nil {
			return nil, err
		}
	}
}

// Small pages exercise continuation tokens in download clients.
func (s *Server) list(w http.ResponseWriter, r *http.Request, bucket string) {
	type entry struct {
		Key  string
		Size int
		ETag string
	}
	result := struct {
		XMLName               xml.Name `xml:"ListBucketResult"`
		IsTruncated           bool
		NextContinuationToken string `xml:",omitempty"`
		Contents              []entry
	}{}
	prefix, token := r.URL.Query().Get("prefix"), r.URL.Query().Get("continuation-token")
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := []string{}
	for full := range s.objects {
		key, ok := strings.CutPrefix(full, bucket+"/")
		if ok && strings.HasPrefix(key, prefix) && key > token {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > 2 {
		keys = keys[:2]
		result.IsTruncated = true
		result.NextContinuationToken = keys[1]
	}
	for _, key := range keys {
		o := s.objects[bucket+"/"+key]
		result.Contents = append(result.Contents, entry{key, len(o.Data), fmt.Sprintf(`"%x"`, md5.Sum(o.Data))})
	}
	w.Header().Set("Content-Type", "application/xml")
	xml.NewEncoder(w).Encode(result)
}
