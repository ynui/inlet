package client

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ynui/inlet/internal/proto"
)

var localClient = &http.Client{Timeout: 60 * time.Second}

func handleReq(c *Conn, data []byte, localPort int, ins *Inspector) {
	req, err := proto.UnmarshalRequest(data)
	if err != nil {
		return
	}
	start := time.Now()

	// build local request
	target := fmt.Sprintf("http://127.0.0.1:%d%s", localPort, req.Path)
	httpReq, err := http.NewRequest(req.Method, target, bytes.NewReader(req.Body))
	if err != nil {
		sendErr(c, req.ID, err)
		return
	}
	for k, vv := range req.Headers {
		for _, v := range vv {
			// avoid hop by hop
			if strings.EqualFold(k, "Connection") || strings.EqualFold(k, "Upgrade") {
				continue
			}
			httpReq.Header.Add(k, v)
		}
	}

	resp, err := localClient.Do(httpReq)
	if err != nil {
		sendErr(c, req.ID, err)
		if ins != nil {
			ins.Add(Event{
				ID:          req.ID,
				Method:      req.Method,
				Path:        req.Path,
				Status:      502,
				Time:        time.Since(start).Milliseconds(),
				Size:        0,
				Remote:      req.Remote,
				ReqHeaders:  req.Headers,
				ResHeaders:  map[string][]string{},
				ReqBody:     bodyPreview(req.Body),
				BodyPreview: err.Error(),
			})
		}
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	hdr := make(map[string][]string, len(resp.Header))
	for k, v := range resp.Header {
		if strings.EqualFold(k, "Connection") || strings.EqualFold(k, "Upgrade") {
			continue
		}
		hdr[k] = append(hdr[k], v...)
	}

	r := &proto.Response{
		ID:      req.ID,
		Status:  resp.StatusCode,
		Headers: hdr,
		Body:    body,
	}
	b, _ := proto.Marshal(proto.TypeRes, r)
	c.Send(b)

	if ins != nil {
		ins.Add(Event{
			ID:          req.ID,
			Method:      req.Method,
			Path:        req.Path,
			Status:      resp.StatusCode,
			Time:        time.Since(start).Milliseconds(),
			Size:        len(body),
			Remote:      req.Remote,
			ReqHeaders:  req.Headers,
			ResHeaders:  hdr,
			ReqBody:     bodyPreview(req.Body),
			BodyPreview: bodyPreview(body),
		})
	}
}

// bodyPreview keeps inspector payloads small; full bodies still get proxied.
func bodyPreview(b []byte) string {
	if len(b) > 4096 {
		return string(b[:4096]) + "\n... (truncated)"
	}
	return string(b)
}

func sendErr(c *Conn, id string, err error) {
	r := &proto.Response{
		ID:      id,
		Status:  502,
		Headers: map[string][]string{"Content-Type": {"text/plain"}},
		Body:    []byte("inlet: " + err.Error()),
	}
	b, _ := proto.Marshal(proto.TypeRes, r)
	c.Send(b)
}
