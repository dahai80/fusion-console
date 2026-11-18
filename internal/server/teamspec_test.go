package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Issue #41: team SPEC collection/display — the create pipeline must forward
// the optional initial SPEC text and the per-member resident context, and
// default the tool allowlist to file_read so members can read the full
// SPEC.md on demand (an empty subagent allowlist means no tools at all).
func TestTeamCreateSpecAndContext(t *testing.T) {
	var regParams map[string]any
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		if method == "teams.register" {
			regParams = params
			return map[string]any{"status": "ok", "name": "spec-team", "namespace_key": "spec-team"}, nil
		}
		return map[string]any{"status": "ok"}, nil
	})
	s, cookie := newTeamTestServer(t, sock)
	body := `{"team":{"name":"spec-team","members":[
{"name":"alice","role":"coder","context":"Role: coder\nRepo: acme/api @ main"},
{"name":"bob","role":"tester","tool_allowlist":["file_read","file_write"],"context":"Role: tester"}]},
"spec":"# SPEC\n## Roles\nalice codes, bob tests"}`
	rec := postTeamCreate(s, cookie, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if regParams == nil {
		t.Fatal("teams.register was not called")
	}
	if spec, _ := regParams["spec"].(string); spec != "# SPEC\n## Roles\nalice codes, bob tests" {
		t.Errorf("register spec = %q, want initial text verbatim", spec)
	}
	def, _ := regParams["definition"].(map[string]any)
	members, _ := def["members"].([]any)
	if len(members) != 2 {
		t.Fatalf("members = %d entries", len(members))
	}
	alice, _ := members[0].(map[string]any)
	if ctx, _ := alice["context"].(string); !strings.Contains(ctx, "Repo: acme/api @ main") {
		t.Errorf("alice context = %q, want worker-config mapping round-trip", ctx)
	}
	allowA, _ := alice["tool_allowlist"].([]any)
	if len(allowA) != 1 || allowA[0] != "file_read" {
		t.Errorf("alice tool_allowlist = %v, want default [file_read]", allowA)
	}
	bob, _ := members[1].(map[string]any)
	allowB, _ := bob["tool_allowlist"].([]any)
	if len(allowB) != 2 || allowB[0] != "file_read" || allowB[1] != "file_write" {
		t.Errorf("bob tool_allowlist = %v, want explicit list untouched", allowB)
	}
}

// TestTeamOpSpecMapping verifies the spec_read/spec_write op mapping
// (issue #41): read passes only the team, write requires non-empty text.
func TestTeamOpSpecMapping(t *testing.T) {
	cases := []struct {
		name       string
		req        teamOpReq
		wantMethod string
		wantParams map[string]any
		wantErr    string
	}{
		{
			name: "spec_read", req: teamOpReq{Op: "spec_read", Team: "beta"},
			wantMethod: "teams.spec_read", wantParams: map[string]any{"team": "beta"},
		},
		{
			name: "spec_write", req: teamOpReq{Op: "spec_write", Team: "beta", Text: "# SPEC\n## Conventions"},
			wantMethod: "teams.spec_write",
			wantParams: map[string]any{"team": "beta", "text": "# SPEC\n## Conventions"},
		},
		{
			name: "spec_write blank text", req: teamOpReq{Op: "spec_write", Team: "beta", Text: "   "},
			wantErr: "text required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method, params, err := teamOpMethod(tc.req, "alpha")
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if method != tc.wantMethod {
				t.Errorf("method = %s, want %s", method, tc.wantMethod)
			}
			if len(params) != len(tc.wantParams) {
				t.Fatalf("params = %v, want %v", params, tc.wantParams)
			}
			for k, v := range tc.wantParams {
				if params[k] != v {
					t.Errorf("params[%s] = %v, want %v", k, params[k], v)
				}
			}
		})
	}
}

// TestTeamOpSpecWriteEndToEnd drives handleTeamOp through the fake daemon
// socket: a spec_write round-trips as teams.spec_write and a daemon-side
// rejection (non-ACTIVE team) surfaces as a 502, never silent.
func TestTeamOpSpecWriteEndToEnd(t *testing.T) {
	var gotText string
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		if method == "teams.spec_write" {
			gotText, _ = params["text"].(string)
			return map[string]any{"status": "ok", "team": params["team"], "bytes": len(gotText)}, nil
		}
		return map[string]any{"status": "error", "message": "spec_write is only allowed on ACTIVE teams"}, nil
	})
	s, cookie := newTeamTestServer(t, sock)
	post := func(body string) (int, string) {
		req := httptest.NewRequest("POST", "/api/team/op", strings.NewReader(body))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		s.handleTeamOp(rec, req)
		return rec.Code, rec.Body.String()
	}
	if code, body := post(`{"op":"spec_write","team":"beta","text":"# SPEC v2\nupdated conventions"}`); code != http.StatusOK {
		t.Fatalf("spec_write status = %d, body = %s", code, body)
	}
	if gotText != "# SPEC v2\nupdated conventions" {
		t.Errorf("daemon text = %q, want full body", gotText)
	}
	// The fake daemon answers everything except spec_write with the
	// non-ACTIVE rejection — a spec_read on it must surface as a 502 with
	// the daemon message verbatim (fail visibly, never silent).
	code, body := post(`{"op":"spec_read","team":"frozen"}`)
	if code != http.StatusBadGateway {
		t.Fatalf("spec_read rejection status = %d, body = %s", code, body)
	}
	if !strings.Contains(body, "spec_write is only allowed on ACTIVE teams") {
		t.Errorf("rejection body = %s, want daemon message verbatim", body)
	}
}
