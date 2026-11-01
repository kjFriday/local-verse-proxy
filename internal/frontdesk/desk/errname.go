package desk

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
)

// ClassName is the Python exception class http.client would have
// raised for err, for the notes frontdesk.py wrote ("nothing answers
// at host:port (ConnectionRefusedError)"). Rare errors read OSError
// (ledger F-14).
func ClassName(err error) string {
	var dns *net.DNSError
	var cert *x509.CertificateInvalidError
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var rec tls.RecordHeaderError
	var alert tls.AlertError
	var cv *tls.CertificateVerificationError
	var ne net.Error
	text := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, os.ErrDeadlineExceeded),
		errors.As(err, &ne) && ne.Timeout(),
		strings.Contains(text, "Client.Timeout"):
		return "TimeoutError"
	case errors.Is(err, syscall.ECONNREFUSED),
		strings.Contains(text, "connection refused"),
		strings.Contains(text, "actively refused"):
		return "ConnectionRefusedError"
	case errors.As(err, &dns):
		return "gaierror"
	case errors.Is(err, syscall.ECONNRESET),
		strings.Contains(text, "connection reset"),
		strings.Contains(text, "forcibly closed"):
		return "ConnectionResetError"
	case errors.As(err, &cv), errors.As(err, &cert),
		errors.As(err, &unknown), errors.As(err, &host):
		return "SSLCertVerificationError"
	case errors.As(err, &rec), errors.As(err, &alert),
		strings.Contains(text, "tls: "):
		return "SSLError"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
		strings.Contains(text, "server closed idle connection"):
		return "RemoteDisconnected"
	case strings.Contains(text, "malformed HTTP"),
		strings.Contains(text, "bad status"):
		return "BadStatusLine"
	}
	return "OSError"
}
