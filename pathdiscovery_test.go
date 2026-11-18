package meshgore

import (
	"bytes"
	"testing"
)

func TestParsePathDiscovery(t *testing.T) {
	// out: 2 hops of 1 byte; in: 0x41 = 1 hop of 2 bytes
	f := []byte{pushPathDiscovery, 0, 1, 2, 3, 4, 5, 6, 2, 0xAA, 0xBB, 0x41, 0xCC, 0xDD}
	p, err := parsePathDiscovery(f)
	if err != nil {
		t.Fatal(err)
	}
	if p.Key != [6]byte{1, 2, 3, 4, 5, 6} || len(p.Out) != 2 || !bytes.Equal(p.Out[1], []byte{0xBB}) {
		t.Errorf("out: %+v", p)
	}
	if len(p.In) != 1 || !bytes.Equal(p.In[0], []byte{0xCC, 0xDD}) {
		t.Errorf("in: %+v", p.In)
	}
}

func TestParsePathDiscoveryDirect(t *testing.T) {
	p, err := parsePathDiscovery([]byte{pushPathDiscovery, 0, 1, 2, 3, 4, 5, 6, 0, 0})
	if err != nil || len(p.Out) != 0 || len(p.In) != 0 {
		t.Errorf("%+v %v", p, err)
	}
}

func TestParsePathDiscoveryBad(t *testing.T) {
	for name, f := range map[string][]byte{
		"short":      {pushPathDiscovery, 0, 1, 2},
		"truncated":  {pushPathDiscovery, 0, 1, 2, 3, 4, 5, 6, 3, 1, 2, 0},
		"reserved":   {pushPathDiscovery, 0, 1, 2, 3, 4, 5, 6, 0xC0, 0},
		"no inbound": {pushPathDiscovery, 0, 1, 2, 3, 4, 5, 6, 0, 0xFF},
	} {
		if _, err := parsePathDiscovery(f); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
