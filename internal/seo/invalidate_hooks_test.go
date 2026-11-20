package seo

import (
	"reflect"
	"testing"
)

func TestSiteInvalidateRunsHooks(t *testing.T) {
	site := NewSite([]byte(testTemplate), testBaseURL, nil, nil)
	var got []int
	site.OnInvalidate(func() { got = append(got, 1) })
	site.OnInvalidate(nil)
	site.OnInvalidate(func() { got = append(got, 2) })
	site.Invalidate()
	if !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("hooks ran %v, want [1 2]", got)
	}
}
