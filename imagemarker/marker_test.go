package imagemarker_test

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/imagemarker"
)

const goodSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func goodRef() imagemarker.Ref {
	return imagemarker.Ref{
		SHA256: goodSHA,
		Mime:   "image/png",
		W:      1568, H: 882,
		OrigW: 2560, OrigH: 1440,
		Bytes: 421888,
		Src:   "/home/ng/shot.png",
	}
}

func TestFormatPinsTheKeyOrderWithTheTolerantTailLast(t *testing.T) {
	got := imagemarker.Format(goodRef())
	want := "[[rig:image sha256=" + goodSHA + " mime=image/png w=1568 h=882 orig=2560x1440 bytes=421888 src=/home/ng/shot.png]]"
	if got != want {
		t.Fatalf("Format = %q, want %q", got, want)
	}
}

func TestFormatIsByteStable(t *testing.T) {
	if a, b := imagemarker.Format(goodRef()), imagemarker.Format(goodRef()); a != b {
		t.Fatalf("Format must be byte-stable across calls: %q vs %q", a, b)
	}
}

func TestParseRoundTripsAFormattedRef(t *testing.T) {
	in := goodRef()
	line := imagemarker.Format(in)
	got, ok := imagemarker.Parse(line)
	if !ok {
		t.Fatalf("Parse refused its own Format: %q", line)
	}
	if got != in {
		t.Fatalf("round trip = %+v, want %+v", got, in)
	}
}

func TestParseKeepsASrcCarryingSpaces(t *testing.T) {
	in := goodRef()
	in.Src = "/home/ng/My Shots/the shot.png"
	got, ok := imagemarker.Parse(imagemarker.Format(in))
	if !ok || got.Src != in.Src {
		t.Fatalf("src with spaces: %+v, %v, want %q", got, ok, in.Src)
	}
}

func TestParseKeepsASrcCarryingTheTerminator(t *testing.T) {
	in := goodRef()
	in.Src = "/tmp/]].png"
	got, ok := imagemarker.Parse(imagemarker.Format(in))
	if !ok || got.Src != in.Src {
		t.Fatalf("src carrying ]]: %+v, %v, want %q", got, ok, in.Src)
	}
}

func TestParseKeepsASrcCarryingAKeyLookalike(t *testing.T) {
	in := goodRef()
	in.Src = "/tmp/bytes=9 src=odd.png"
	got, ok := imagemarker.Parse(imagemarker.Format(in))
	if !ok || got.Src != in.Src {
		t.Fatalf("src carrying a key lookalike: %+v, %v, want %q", got, ok, in.Src)
	}
}

func TestParseRefusesLinesThatAreNotMarkers(t *testing.T) {
	for _, line := range []string{
		"",
		"(no matches for /re/ under /tmp)",
		"[[rig:image]]",
		"[[rig:img sha256=" + goodSHA + " mime=image/png w=1 h=1 orig=1x1 bytes=1 src=/a]]",
		"[rig:image sha256=" + goodSHA + " mime=image/png w=1 h=1 orig=1x1 bytes=1 src=/a]]",
		"[[rig:image sha256=" + goodSHA + " mime=image/png w=1 h=1 orig=1x1 bytes=1 src=/a",
		"prefix [[rig:image sha256=" + goodSHA + " mime=image/png w=1 h=1 orig=1x1 bytes=1 src=/a]]",
		"[[rig:image sha256=" + goodSHA + " mime=image/png w=1 h=1 orig=1x1 bytes=1 src=/a]] suffix",
	} {
		if got, ok := imagemarker.Parse(line); ok {
			t.Fatalf("Parse(%q) = %+v, want refused", line, got)
		}
	}
}

func TestParseRefusesEveryBrokenField(t *testing.T) {
	for _, broken := range brokenMarkers() {
		if got, ok := imagemarker.Parse(broken); ok {
			t.Fatalf("Parse accepted a broken marker: %q gave %+v", broken, got)
		}
	}
}

func brokenMarkers() []string {
	good := imagemarker.Format(goodRef())
	return []string{
		strings.Replace(good, " sha256="+goodSHA, "", 1),
		strings.Replace(good, goodSHA, goodSHA[:63], 1),
		strings.Replace(good, goodSHA, strings.ToUpper(goodSHA), 1),
		strings.Replace(good, goodSHA, goodSHA[:62]+"zz", 1),
		strings.Replace(good, goodSHA, goodSHA+"ab", 1),
		strings.Replace(good, " mime=image/png", "", 1),
		strings.Replace(good, " mime=image/png", " mime=png", 1),
		strings.Replace(good, " w=1568", "", 1),
		strings.Replace(good, " h=882", "", 1),
		strings.Replace(good, " orig=2560x1440", "", 1),
		strings.Replace(good, " orig=2560x1440", " orig=2560", 1),
		strings.Replace(good, " bytes=421888", "", 1),
		strings.Replace(good, " src=/home/ng/shot.png", "", 1),
		strings.Replace(good, " src=/home/ng/shot.png", " src=", 1),
		strings.Replace(good, "w=1568 h=882", "h=882 w=1568", 1),
		strings.Replace(good, "w=1568", "w=0", 1),
		strings.Replace(good, "h=882", "h=0", 1),
		strings.Replace(good, "orig=2560x1440", "orig=2560x0", 1),
		strings.Replace(good, "bytes=421888", "bytes=0", 1),
		strings.Replace(good, "w=1568", "w=1.5", 1),
		strings.Replace(good, "w=1568", "w=-1568", 1),
	}
}

func TestFindTakesTheMarkerLineOutOfAToolResult(t *testing.T) {
	marker := imagemarker.Format(goodRef())
	got, ok := imagemarker.Find("some header line\n" + marker + "\n[a trailing note]")
	if !ok {
		t.Fatal("Find must locate the marker line inside a multi-line reply")
	}
	if got.Src != "/home/ng/shot.png" || got.W != 1568 {
		t.Fatalf("Find = %+v, want the marker's ref", got)
	}
	if _, ok := imagemarker.Find("no image here\nstill none"); ok {
		t.Fatal("Find must refuse text with no marker")
	}
	if _, ok := imagemarker.Find(""); ok {
		t.Fatal("Find must refuse the empty reply")
	}
}

func TestFindTakesTheFirstMarkerOfSeveral(t *testing.T) {
	second := goodRef()
	second.SHA256 = strings.Repeat("f", 64)
	second.Src = "/tmp/second.png"
	line := imagemarker.Format(goodRef()) + "\n" + imagemarker.Format(second)
	got, ok := imagemarker.Find(line)
	if !ok || got.Src != "/home/ng/shot.png" {
		t.Fatalf("Find = %+v, %v, want the first marker", got, ok)
	}
}

func TestBlobPathIsTheAddressUnderTheDir(t *testing.T) {
	got := imagemarker.BlobPath("/home/ng/.rig/blobs", goodSHA)
	if !strings.HasSuffix(got, goodSHA) || strings.Contains(strings.TrimSuffix(got, goodSHA), goodSHA) {
		t.Fatalf("BlobPath = %q, want the dir and the sha once", got)
	}
	if strings.ContainsRune(strings.TrimPrefix(got, "/home/ng/.rig/blobs/"), '.') {
		t.Fatalf("the address carries no extension: %q", got)
	}
}

func TestBlobsDirNamesTheStoreUnderTheHome(t *testing.T) {
	if got := imagemarker.BlobsDir("/home/ng/.rig"); got != "/home/ng/.rig/blobs" {
		t.Fatalf("BlobsDir = %q", got)
	}
}

func TestBlobPathRefusesAnAddressShapedInput(t *testing.T) {
	for _, bad := range []string{"", goodSHA[:10], strings.Repeat("f", 64) + "/", "../" + goodSHA} {
		if got := imagemarker.BlobPath("/h/blobs", bad); got != "" {
			t.Fatalf("BlobPath(%q) = %q, want refused", bad, got)
		}
	}
}

func TestParseAcceptsASHA256OfTheRightShape(t *testing.T) {
	if _, err := hex.DecodeString(goodSHA); err != nil {
		t.Fatalf("the fixture is not hex: %v", err)
	}
	if _, ok := imagemarker.Parse(imagemarker.Format(goodRef())); !ok {
		t.Fatal("the well-formed marker must parse")
	}
}
