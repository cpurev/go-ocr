package ocr

import (
	"reflect"
	"testing"
)

func TestMissingLanguages(t *testing.T) {
	listing := "List of available languages in \"/usr/share/tessdata/\" (2):\neng\nosd\n"

	if got := missingLanguages("swe+eng", listing); !reflect.DeepEqual(got, []string{"swe"}) {
		t.Errorf("missing = %v, want [swe]", got)
	}
	if got := missingLanguages("eng", listing); got != nil {
		t.Errorf("missing = %v, want none", got)
	}
}
