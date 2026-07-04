package source

import (
	"bufio"
	"encoding/gob"
	"errors"
	"io"
	"net/rpc"
)

// // // // // // // // // //

// errRPCMessageTooLarge means a brother gob message exceeded the per-message budget.
var errRPCMessageTooLarge = errors.New("brother rpc message exceeds size budget")

// // // // // // // // // //

type limitedReaderObj struct {
	r io.Reader
	n int64
}

func (l *limitedReaderObj) reset(budget int64) { l.n = budget }

// Read returns at most the remaining budget; exhausting it returns errRPCMessageTooLarge.
func (l *limitedReaderObj) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, errRPCMessageTooLarge
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	m, err := l.r.Read(p)
	l.n -= int64(m)
	return m, err
}

// // // // // // // // // //

type boundedGobCodecObj struct {
	conn      io.ReadWriteCloser
	limited   *limitedReaderObj
	dec       *gob.Decoder
	enc       *gob.Encoder
	encBuf    *bufio.Writer
	headerCap int64
	bodyCap   int64
}

func newBoundedGobCodec(conn io.ReadWriteCloser, headerCap, bodyCap int64) *boundedGobCodecObj {
	limited := &limitedReaderObj{r: conn}
	encBuf := bufio.NewWriter(conn)
	return &boundedGobCodecObj{
		conn:      conn,
		limited:   limited,
		dec:       gob.NewDecoder(limited),
		enc:       gob.NewEncoder(encBuf),
		encBuf:    encBuf,
		headerCap: headerCap,
		bodyCap:   bodyCap,
	}
}

// WriteRequest gob-encodes the request header and body, then flushes the buffer.
func (c *boundedGobCodecObj) WriteRequest(reqObj *rpc.Request, body any) error {
	if err := c.enc.Encode(reqObj); err != nil {
		return err
	}
	if err := c.enc.Encode(body); err != nil {
		return err
	}
	return c.encBuf.Flush()
}

// ReadResponseHeader decodes the response header under the headerCap budget.
func (c *boundedGobCodecObj) ReadResponseHeader(respObj *rpc.Response) error {
	c.limited.reset(c.headerCap)
	return c.dec.Decode(respObj)
}

// ReadResponseBody decodes the response body under the bodyCap budget.
func (c *boundedGobCodecObj) ReadResponseBody(body any) error {
	c.limited.reset(c.bodyCap)
	return c.dec.Decode(body)
}

// Close closes the underlying connection.
func (c *boundedGobCodecObj) Close() error {
	return c.conn.Close()
}
