package cover

import (
	"os"
	"testing"
)

func TestBeggar(t *testing.T) {
	if _, err := os.Stat("../../beggar.azw3"); err != nil {
		t.Skip("no sample book")
	}
	Dir = t.TempDir()
	target, wrote, err := Ensure("../../beggar.azw3", "", nil)
	if err != nil || !wrote {
		t.Fatal("no cover", err)
	}
	b, _ := os.ReadFile(target)
	os.WriteFile("../../beggar-thumb.jpg", b, 0o644)
	if _, wrote, _ := Ensure("../../beggar.azw3", target, nil); wrote {
		t.Fatal("rewrote an existing cover")
	}
}
