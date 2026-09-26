package nodestate

import (
	"testing"
	"time"
)

func TestParseDockerPS(t *testing.T) {
	out := []byte(`{"Names":"api","Image":"ghcr.io/o/api:v1","State":"running","Status":"Up 3 hours","Ports":"0.0.0.0:20000->8000/tcp","Labels":"asdl.managed=true,asdl.project=p-1,org.opencontainers.image.source=x"}
{"Names":"postgres","Image":"postgres:16","State":"exited","Status":"Exited (0) 2 days ago","Ports":"","Labels":""}
not json
`)
	got := parseDockerPS(out)
	if len(got) != 2 {
		t.Fatalf("got %d containers", len(got))
	}
	if !got[0].Managed || got[0].ProjectID != "p-1" || got[0].State != "running" {
		t.Errorf("hub container = %+v", got[0])
	}
	if got[1].Managed || got[1].ProjectID != "" {
		t.Errorf("other container = %+v", got[1])
	}
}

func TestValidContainerName(t *testing.T) {
	for name, ok := range map[string]bool{"api": true, "simplebanking-backend": true, "a.b_c": true, "": false, "-x": false, "x; rm -rf /": false, "$(id)": false} {
		if ValidContainerName(name) != ok {
			t.Errorf("ValidContainerName(%q) = %v", name, !ok)
		}
	}
}

func TestLatestHandshake(t *testing.T) {
	out := []byte("asdl0\tPEERKEY1=\t1790000000\nasdl0\tPEERKEY2=\t1790000500\n")
	if got := latestHandshake(out); !got.Equal(time.Unix(1790000500, 0)) {
		t.Errorf("got %v", got)
	}
	if !latestHandshake([]byte("PEER=\t0\n")).IsZero() {
		t.Error("never = zero time")
	}
}
