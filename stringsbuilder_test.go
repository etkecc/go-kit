package kit

import (
	"testing"
	"unicode/utf8"
)

func TestNewStringsBuilderEmpty(t *testing.T) {
	sb := NewStringsBuilder()
	if sb.Len() != 0 {
		t.Fatalf("expected Len 0, got %d", sb.Len())
	}
	if sb.String() != "" {
		t.Fatalf("expected empty String, got %q", sb.String())
	}
	if sb.Cap() < 0 {
		t.Fatalf("expected non-negative Cap, got %d", sb.Cap())
	}
}

func TestSAndString(t *testing.T) {
	sb := NewStringsBuilder()
	got := sb.S("hello").S(" ").S("world").String()
	if got != "hello world" {
		t.Fatalf("expected %q, got %q", "hello world", got)
	}
	if sb.Len() != len(got) {
		t.Fatalf("expected Len %d, got %d", len(got), sb.Len())
	}
}

func TestBAndR(t *testing.T) {
	sb := NewStringsBuilder()
	sb.B('A').B(' ').R('€')
	want := "A €"
	if sb.String() != want {
		t.Fatalf("expected %q, got %q", want, sb.String())
	}
	if sb.Len() != len(want) {
		t.Fatalf("expected Len %d, got %d", len(want), sb.Len())
	}
}

func TestWriteMethods(t *testing.T) {
	sb := NewStringsBuilder()

	n, err := sb.WriteString("ab")
	if err != nil || n != 2 {
		t.Fatalf("WriteString: expected n=2 err=nil, got n=%d err=%v", n, err)
	}

	n, err = sb.Write([]byte("cd"))
	if err != nil || n != 2 {
		t.Fatalf("Write: expected n=2 err=nil, got n=%d err=%v", n, err)
	}

	r := '界'
	n, err = sb.WriteRune(r)
	if err != nil || n != utf8.RuneLen(r) {
		t.Fatalf("WriteRune: expected n=%d err=nil, got n=%d err=%v", utf8.RuneLen(r), n, err)
	}

	want := "abcd界" //nolint:gosmopolitan // that's the point
	if sb.String() != want {
		t.Fatalf("expected %q, got %q", want, sb.String())
	}
}

func TestGrowAndCapAndLen(t *testing.T) {
	sb := NewStringsBuilder()
	sb.S("abc")
	beforeLen := sb.Len()
	beforeCap := sb.Cap()

	sb.Grow(100)
	if sb.Cap() < beforeLen+100 {
		t.Fatalf("expected Cap >= %d, got %d (beforeCap=%d)", beforeLen+100, sb.Cap(), beforeCap)
	}

	n, err := sb.Write(make([]byte, 100))
	if err != nil || n != 100 {
		t.Fatalf("Write after Grow: expected n=100 err=nil, got n=%d err=%v", n, err)
	}
	if sb.Len() != beforeLen+100 {
		t.Fatalf("expected Len %d, got %d", beforeLen+100, sb.Len())
	}
}

func TestResetReuse(t *testing.T) {
	sb := NewStringsBuilder()
	sb.S("data")
	sb.Reset()
	if sb.Len() != 0 || sb.String() != "" {
		t.Fatalf("expected empty after Reset, got Len=%d String=%q", sb.Len(), sb.String())
	}
	sb.S("x")
	if sb.String() != "x" {
		t.Fatalf("expected %q after reuse, got %q", "x", sb.String())
	}
}

func TestChainReturnsSamePointer(t *testing.T) {
	sb := NewStringsBuilder()
	p := sb.S("a")
	if p != &sb {
		t.Fatalf("expected S to return receiver pointer")
	}
	q := p.B('b').R('c').Grow(5).Reset().S("z")
	if q != &sb {
		t.Fatalf("expected chained methods to return receiver pointer")
	}
	if sb.String() != "z" {
		t.Fatalf("expected %q after chain, got %q", "z", sb.String())
	}
}

func TestUnwrapString(t *testing.T) {
	sb := NewStringsBuilder()
	sb.S("hello")
	u := sb.Unwrap()
	if u.String() != "hello" {
		t.Fatalf("expected unwrapped String %q, got %q", "hello", u.String())
	}
}
