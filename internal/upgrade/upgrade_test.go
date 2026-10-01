package upgrade

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/local/mikrotik-mirror/internal/routeros"
)

// fakeRouter implements just enough of the REST API for the flow.
type fakeRouter struct {
	channel   string
	sources   []map[string]string
	pkgs      []map[string]string
	bound     map[string]string
	downloads string
}

func (f *fakeRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if u, p, _ := r.BasicAuth(); u != "admin" || p != "pw" {
		w.WriteHeader(401)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/rest")
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	out := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case path == "/system/resource":
		out(map[string]string{"version": "7.20.7 (long-term)", "architecture-name": "smips"})
	case path == "/system/package":
		out([]map[string]string{{"name": "routeros"}, {"name": "wireless"}, {"name": "dude", "disabled": "true"}})
	case path == "/system/license":
		out(map[string]string{"system-id": "SID1"})
	case path == "/system/package/update":
		out(map[string]string{"channel": f.channel})
	case path == sourcePath && r.Method == "GET":
		out(f.sources)
	case strings.HasPrefix(path, sourcePath):
		f.bound = body
		f.sources = []map[string]string{{".id": "*1", "address": body["address"], "user": body["user"]}}
	case path == luPath+"/refresh":
	case path == luPath && r.Method == "GET":
		out(f.pkgs)
	case path == luPath+"/download":
		f.downloads = body["numbers"]
		for _, p := range f.pkgs {
			p["status"] = "downloaded"
		}
	default:
		w.WriteHeader(404)
	}
}

func run(t *testing.T, f *fakeRouter, apply bool) Result {
	srv := httptest.NewServer(f)
	defer srv.Close()
	c, err := routeros.Dial(context.Background(), strings.TrimPrefix(srv.URL, "http://"), "admin", "pw", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return Router(context.Background(), c, Options{
		Mirror:          "1.2.3.4",
		Accounts:        map[string]Account{"long-term": {"longterm", "lt"}, "stable": {"stable", "st"}},
		Apply:           apply,
		DownloadTimeout: time.Second,
	})
}

func TestApplyBindsAndDownloads(t *testing.T) {
	f := &fakeRouter{channel: "long-term", pkgs: []map[string]string{
		{".id": "*1", "name": "routeros", "version": "7.23.7", "status": "available"},
		{".id": "*2", "name": "wireless", "version": "7.23.7", "status": "available"},
		{".id": "*3", "name": "dude", "version": "7.23.7", "status": "available"},
	}}
	r := run(t, f, true)
	if r.Status != Staged {
		t.Fatalf("%+v", r)
	}
	if f.bound["user"] != "longterm" || f.bound["password"] != "lt" || f.bound["address"] != "1.2.3.4" {
		t.Errorf("bound %v", f.bound)
	}
	if f.downloads != "*1,*2" {
		t.Errorf("downloads %q", f.downloads)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	f := &fakeRouter{channel: "stable"}
	if r := run(t, f, false); r.Status != Planned || f.bound != nil {
		t.Fatalf("%+v bound=%v", r, f.bound)
	}
}

func TestUpToDateAndAlreadyBound(t *testing.T) {
	f := &fakeRouter{channel: "stable",
		sources: []map[string]string{{".id": "*1", "address": "1.2.3.4", "user": "stable"}},
		pkgs:    []map[string]string{{".id": "*1", "name": "routeros", "version": "7.23.7", "status": "installed"}}}
	if r := run(t, f, true); r.Status != UpToDate || f.bound != nil || f.downloads != "" {
		t.Fatalf("%+v", r)
	}
}

func TestUnmirroredChannelSkipped(t *testing.T) {
	if r := run(t, &fakeRouter{channel: "testing"}, true); r.Status != Skipped {
		t.Fatalf("%+v", r)
	}
}

func TestBadLoginIsAuthError(t *testing.T) {
	srv := httptest.NewServer(&fakeRouter{})
	defer srv.Close()
	_, err := routeros.Dial(context.Background(), strings.TrimPrefix(srv.URL, "http://"), "admin", "wrong", 2*time.Second)
	if err == nil || err != routeros.ErrAuth {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestSameRouterTwoAddressesProcessedOnce(t *testing.T) {
	f := &fakeRouter{channel: "stable", pkgs: []map[string]string{{".id": "*1", "name": "routeros", "version": "7.23.7", "status": "available"}}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	claims := &Claims{}
	var got []Status
	for _, name := range []string{"lan", "public"} {
		c, err := routeros.Dial(context.Background(), strings.TrimPrefix(srv.URL, "http://"), "admin", "pw", 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		r := Router(context.Background(), c, Options{Mirror: "1.2.3.4", Apply: true, Claims: claims, Name: name,
			Accounts: map[string]Account{"stable": {"stable", "st"}}, DownloadTimeout: time.Second})
		got = append(got, r.Status)
	}
	if got[0] != Staged || got[1] != Skipped {
		t.Fatalf("%v", got)
	}
}

func runConfirm(t *testing.T, f *fakeRouter, confirm func(string) bool) (Result, []string) {
	srv := httptest.NewServer(f)
	defer srv.Close()
	c, err := routeros.Dial(context.Background(), strings.TrimPrefix(srv.URL, "http://"), "admin", "pw", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	r := Router(context.Background(), c, Options{Mirror: "1.2.3.4", Apply: true, DownloadTimeout: time.Second,
		Accounts: map[string]Account{"stable": {"stable", "st"}},
		Confirm:  func(q string) bool { asked = append(asked, q); return confirm(q) }})
	return r, asked
}

func TestDeclineBindChangesNothing(t *testing.T) {
	f := &fakeRouter{channel: "stable"}
	r, asked := runConfirm(t, f, func(string) bool { return false })
	if r.Status != Skipped || f.bound != nil || len(asked) != 1 || !strings.Contains(asked[0], "绑定") {
		t.Fatalf("%+v bound=%v asked=%q", r, f.bound, asked)
	}
}

func TestAlreadyBoundAsksOnlyAboutDownload(t *testing.T) {
	f := &fakeRouter{channel: "stable",
		sources: []map[string]string{{".id": "*1", "address": "1.2.3.4", "user": "stable"}},
		pkgs:    []map[string]string{{".id": "*1", "name": "routeros", "version": "7.23.7", "status": "available"}}}
	r, asked := runConfirm(t, f, func(string) bool { return false })
	if r.Status != Skipped || f.downloads != "" || len(asked) != 1 || !strings.Contains(asked[0], "下载") {
		t.Fatalf("%+v asked=%q", r, asked)
	}
}

func TestBindThenDownloadAskedInOrder(t *testing.T) {
	f := &fakeRouter{channel: "stable",
		pkgs: []map[string]string{{".id": "*1", "name": "routeros", "version": "7.23.7", "status": "available"}}}
	r, asked := runConfirm(t, f, func(string) bool { return true })
	if r.Status != Staged || len(asked) != 2 || !strings.Contains(asked[0], "绑定") || !strings.Contains(asked[1], "下载") {
		t.Fatalf("%+v asked=%q", r, asked)
	}
}
