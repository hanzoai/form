package main

// client.go — /v1/form over HTTP with an IAM bearer. A 429, a 5xx or a dropped
// connection is retried with backoff; anything else is the answer. The bearer is
// sent in its header and nowhere else.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// client calls one API.
type client struct {
	ctx   context.Context
	base  string
	token string
	// org is the org the records land in, named on every call: a bearer that
	// administers several orgs is pointed at this one, never at its home by default.
	org  string
	http *http.Client
	// pause is the first wait before a retry; it doubles, to at most a minute.
	pause time.Duration
	// tries is how many times a call is made before its error is the answer.
	tries int
}

// call sends in as JSON and decodes the answer into out, when out is not nil.
func (c *client) call(method, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	wait := c.pause
	for try := 1; ; try++ {
		req, err := http.NewRequestWithContext(c.ctx, method, c.base+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("X-Org-Id", c.org)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err == nil {
			b, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			switch code := resp.StatusCode; {
			case rerr != nil:
				err = rerr
			case code/100 == 2:
				if out == nil {
					return nil
				}
				return json.Unmarshal(b, out)
			case code == http.StatusTooManyRequests || code >= 500:
				err = fmt.Errorf("%s %s: %d %s", method, path, code, b[:min(len(b), 300)])
				if s, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && time.Duration(s)*time.Second > wait {
					wait = time.Duration(s) * time.Second
				}
			default:
				return fmt.Errorf("%s %s: %d %s", method, path, code, b[:min(len(b), 300)])
			}
		}
		if try == c.tries {
			return err
		}
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case <-time.After(min(wait, time.Minute)):
		}
		wait = min(wait*2, time.Minute)
	}
}
