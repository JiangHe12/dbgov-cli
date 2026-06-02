package safety

import "testing"

func TestAllowFlagsUseBareNames(t *testing.T) {
	tests := map[AllowFlag]string{
		AllowDestructive:     "allow-destructive",
		AllowNoWhere:         "allow-no-where",
		AllowProductionPrune: "allow-production-prune",
	}
	for flag, want := range tests {
		if string(flag) != want {
			t.Fatalf("AllowFlag = %q, want %q", flag, want)
		}
	}
}

func TestFacadeUsesCoreSafety(t *testing.T) {
	if got := EffectiveRisk(R1, ContextMeta{Protected: true}); got != R2 {
		t.Fatalf("EffectiveRisk(R1 protected) = %v, want R2", got)
	}
	if err := Authorize(R1, Options{NonInteractive: true}); err == nil {
		t.Fatal("R1 non-interactive without --yes should be denied by core safety")
	}
}
