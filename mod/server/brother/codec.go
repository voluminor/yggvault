package brother

import (
	"bufio"
	"encoding/gob"
	"errors"
	"io"
	"net/rpc"
)

// // // // // // // // // //

const (
	cRequestHeaderCap = 1 << 20
	cRequestBodyCap   = 16 << 20
)

var errRequestTooLarge = errors.New("brother rpc request exceeds size budget")

// // // // // // // // // //

type limitedReaderObj struct {
	r io.Reader
	n int64
}

func (l *limitedReaderObj) reset(budget int64) { l.n = budget }

// Read returns at most the remaining budget; exhausting it returns errRequestTooLarge.
func (l *limitedReaderObj) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, errRequestTooLarge
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	m, err := l.r.Read(p)
	l.n -= int64(m)
	return m, err
}

// // // // // // // // // //

type boundedCodecObj struct {
	conn      io.ReadWriteCloser
	limited   *limitedReaderObj
	dec       *gob.Decoder
	enc       *gob.Encoder
	encBuf    *bufio.Writer
	headerCap int64
	bodyCap   int64
	closed    bool
}

func newBoundedCodec(conn io.ReadWriteCloser, headerCap, bodyCap int64) *boundedCodecObj {
	limited := &limitedReaderObj{r: conn}
	encBuf := bufio.NewWriter(conn)
	return &boundedCodecObj{
		conn:      conn,
		limited:   limited,
		dec:       gob.NewDecoder(limited),
		enc:       gob.NewEncoder(encBuf),
		encBuf:    encBuf,
		headerCap: headerCap,
		bodyCap:   bodyCap,
	}
}

// ReadRequestHeader decodes the request header under the headerCap budget.
func (c *boundedCodecObj) ReadRequestHeader(reqObj *rpc.Request) error {
	c.limited.reset(c.headerCap)
	return c.dec.Decode(reqObj)
}

// ReadRequestBody decodes the request body under the bodyCap budget.
func (c *boundedCodecObj) ReadRequestBody(body any) error {
	c.limited.reset(c.bodyCap)
	return c.dec.Decode(body)
}

// WriteResponse gob-encodes the response header and body. Any encode error desynchronizes the
// gob stream, so the connection is closed unconditionally without flushing partial bytes.
func (c *boundedCodecObj) WriteResponse(respObj *rpc.Response, body any) error {
	if err := c.enc.Encode(respObj); err != nil {
		_ = c.Close()
		return err
	}
	if err := c.enc.Encode(body); err != nil {
		_ = c.Close()
		return err
	}
	return c.encBuf.Flush()
}

// Close closes the connection once.
func (c *boundedCodecObj) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	return c.conn.Close()
}
