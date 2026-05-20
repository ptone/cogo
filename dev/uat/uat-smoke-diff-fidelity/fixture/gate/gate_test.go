package gate

import "testing"

func TestCheckBash_Allowed(t *testing.T) {
	g := New(true)
	if err := g.CheckBash("ls"); err != nil {
		t.Fatalf("CheckBash should pass when allow=true; got %v", err)
	}
}

func TestCheckBash_Denied(t *testing.T) {
	g := New(false)
	if err := g.CheckBash("rm -rf /"); err == nil {
		t.Fatalf("CheckBash should deny when allow=false; got nil")
	}
}

func TestCheckGeneric_Allowed(t *testing.T) {
	g := New(true)
	if err := g.CheckGeneric("write_file", "/tmp/x"); err != nil {
		t.Fatalf("CheckGeneric should pass when allow=true; got %v", err)
	}
}
