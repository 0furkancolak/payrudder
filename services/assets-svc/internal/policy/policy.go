// Package policy holds assets-svc's rules: which content types a purpose accepts, size limits, part sizing, filename
// sanitising and content sniffing (docs/03-services/assets-svc.md#validation-and-security).
package policy

import (
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
)

// Error codes carried in the ErrorInfo reason of a gRPC status and in per-file results.
const (
	UnsupportedContentType = "UNSUPPORTED_CONTENT_TYPE"
	ContentTypeMismatch    = "CONTENT_TYPE_MISMATCH"
	FileTooLarge           = "FILE_TOO_LARGE"
	StorageQuotaExceeded   = "STORAGE_QUOTA_EXCEEDED"
	InvalidFilename        = "INVALID_FILENAME"
	PartMismatch           = "PART_MISMATCH"
	SizeMismatch           = "SIZE_MISMATCH"
)

// Sizes.
const (
	MiB = int64(1) << 20
	GiB = int64(1) << 30
	// MaxFileSize is the hard cap of a single file in v1.
	MaxFileSize = 5 * GiB
	// MinPartSize is S3's lower bound for every part but the last; MaxParts its upper bound on the part count.
	MinPartSize = 8 * MiB
	MaxParts    = 10000
)

type rule struct {
	types   map[string]bool
	maxSize int64
}

// DefaultGeneralTypes is the allowlist of the general purpose (configurable).
var DefaultGeneralTypes = []string{"application/pdf", "image/png", "image/jpeg", "text/csv", "application/json", "application/zip"}

// Policy is the set of rules.
type Policy struct {
	rules map[assetsv1.Purpose]rule
}

// New builds the policy; general lists the content types the general purpose accepts.
func New(general []string) Policy {
	set := func(t ...string) map[string]bool {
		m := map[string]bool{}
		for _, x := range t {
			m[strings.ToLower(x)] = true
		}
		return m
	}
	return Policy{rules: map[assetsv1.Purpose]rule{
		assetsv1.Purpose_PURPOSE_INVOICE_LOGO:       {set("image/png", "image/jpeg"), 2 * MiB},
		assetsv1.Purpose_PURPOSE_INVOICE_ATTACHMENT: {set("application/pdf"), 25 * MiB},
		assetsv1.Purpose_PURPOSE_GENERAL:            {set(general...), MaxFileSize},
	}}
}

// PurposeName is the database spelling of a purpose.
func PurposeName(p assetsv1.Purpose) string {
	switch p {
	case assetsv1.Purpose_PURPOSE_INVOICE_LOGO:
		return "invoice_logo"
	case assetsv1.Purpose_PURPOSE_INVOICE_ATTACHMENT:
		return "invoice_attachment"
	case assetsv1.Purpose_PURPOSE_GENERAL:
		return "general"
	}
	return ""
}

// PurposeOf parses the database spelling.
func PurposeOf(s string) assetsv1.Purpose {
	switch s {
	case "invoice_logo":
		return assetsv1.Purpose_PURPOSE_INVOICE_LOGO
	case "invoice_attachment":
		return assetsv1.Purpose_PURPOSE_INVOICE_ATTACHMENT
	case "general":
		return assetsv1.Purpose_PURPOSE_GENERAL
	}
	return assetsv1.Purpose_PURPOSE_UNSPECIFIED
}

// Violation is a rejected upload with its error code.
type Violation struct{ Code, Message string }

func (v *Violation) Error() string { return v.Code + ": " + v.Message }

// NormalizeType lower-cases a content type and drops its parameters.
func NormalizeType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

// Check validates a declared file against its purpose.
func (p Policy) Check(purpose assetsv1.Purpose, contentType string, size int64) (string, *Violation) {
	r, ok := p.rules[purpose]
	if !ok {
		return "", &Violation{UnsupportedContentType, "unknown purpose"}
	}
	ct := NormalizeType(contentType)
	if !r.types[ct] {
		return "", &Violation{UnsupportedContentType, "content type " + ct + " is not allowed for " + PurposeName(purpose)}
	}
	if size > r.maxSize {
		return "", &Violation{FileTooLarge, PurposeName(purpose) + " files are limited to " + humanSize(r.maxSize)}
	}
	return ct, nil
}

// MaxSize returns the size limit of a purpose.
func (p Policy) MaxSize(purpose assetsv1.Purpose) int64 { return p.rules[purpose].maxSize }

func humanSize(n int64) string {
	switch {
	case n >= GiB:
		return itoa(n/GiB) + " GiB"
	default:
		return itoa(n/MiB) + " MiB"
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// PartSize is max(8 MiB, ceil(size / 10 000)): S3's 5 MiB minimum and 10 000-part maximum both hold. The last part may be
// smaller.
func PartSize(size int64) int64 {
	ps := (size + MaxParts - 1) / MaxParts
	return max(MinPartSize, ps)
}

// PartCount is the number of parts of a file of the given size (at least one).
func PartCount(size, partSize int64) int32 {
	if size <= 0 {
		return 1
	}
	return int32((size + partSize - 1) / partSize) //nolint:gosec // ≤ 10 000
}

// ErrFilename means a filename cannot be used.
var ErrFilename = errors.New("invalid filename")

// SanitizeFilename returns an NFC-normalised display name without path separators or control characters, at most 255
// bytes. The name is display-only: it never appears in an object key.
func SanitizeFilename(name string) (string, error) {
	name = norm.NFC.String(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == 0:
			return "", ErrFilename
		case unicode.IsControl(r):
			return "", ErrFilename
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == ".." {
		return "", ErrFilename
	}
	if len(out) > 255 {
		// Cut on a rune boundary.
		out = out[:255]
		for !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
	}
	return out, nil
}

// Sniff checks the first bytes of a file against the declared content type. Text formats (csv, json) are accepted when
// the sniffed type is text, because content sniffing cannot tell them apart.
func Sniff(declared string, head []byte) bool {
	declared = NormalizeType(declared)
	got := NormalizeType(http.DetectContentType(head))
	switch declared {
	case "text/csv", "application/json":
		return strings.HasPrefix(got, "text/plain")
	case "application/zip":
		return got == "application/zip"
	}
	return got == declared
}
