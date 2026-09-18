package config

import "testing"

func TestGetBoolRejectsATypo(t *testing.T) {
	t.Setenv("ZZ_BOOL", "fasle")
	if _, err := getBool("ZZ_BOOL", true); err == nil {
		t.Fatal("getBool accepted \"fasle\"; it used to fall back to the default silently")
	}

	t.Setenv("ZZ_BOOL", "off")
	if v, err := getBool("ZZ_BOOL", true); err != nil || v {
		t.Fatalf("getBool(off) = %v, %v; want false", v, err)
	}
}

func TestGetDurationRejectsNonPositive(t *testing.T) {
	for _, raw := range []string{"0s", "-40s"} {
		t.Setenv("ZZ_DUR", raw)
		if _, err := getDuration("ZZ_DUR", 1); err == nil {
			t.Errorf("getDuration accepted %q", raw)
		}
	}
}
