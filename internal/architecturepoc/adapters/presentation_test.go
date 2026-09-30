package adapters

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
)

func TestReaderTokenAndNamespacedTargetsRoundTrip(t *testing.T) {
	token := application.ReaderToken{
		Target: application.ConnectionTarget("lab/a"), Generation: 9, RefreshSequence: 4,
		EnvironmentID: "env-1", OwnerInstanceID: "owner-1", Revision: 3,
		CanCreateBoundFixture: true, CanRenameFixture: true,
	}
	encoded, err := EncodeReaderToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "rt1.") || strings.Contains(encoded, "=") {
		t.Fatalf("token encoding = %q", encoded)
	}
	decoded, err := DecodeReaderToken(encoded)
	if err != nil || decoded != token {
		t.Fatalf("token round trip = %+v, %v", decoded, err)
	}
	target := application.SessionTarget{Owner: token.Target, SessionID: "same/id"}
	targetID := EncodeSessionTarget(target)
	if targetID != "connection/lab%2Fa/same%2Fid" {
		t.Fatalf("target id = %q", targetID)
	}
	parsed, err := DecodeSessionTarget(targetID)
	if err != nil || parsed != target {
		t.Fatalf("target round trip = %+v, %v", parsed, err)
	}
	if device := EncodeSessionTarget(application.SessionTarget{Owner: application.DeviceTarget(), SessionID: "same/id"}); device == targetID {
		t.Fatalf("local and remote target collided: %q", device)
	}
}

func TestInventoryDocumentCarriesTargetContextAndFailureState(t *testing.T) {
	token := application.ReaderToken{Target: application.ConnectionTarget("lab"), Generation: 2, RefreshSequence: 3, EnvironmentID: "env", OwnerInstanceID: "owner", Revision: 3, CanRenameFixture: true}
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	inventory := application.Inventory{Scope: application.ScopeWorkspace, Targets: []application.TargetProjection{{
		Target: token.Target, Label: "Lab", Token: token,
		Owner:     application.OwnerInfo{InstanceID: "owner", EnvironmentID: "env", Revision: 3, Capabilities: []string{application.CapabilitySessionsRead, application.CapabilityFixtureRename}},
		Freshness: application.Freshness{State: application.FreshnessStale, RefreshedAt: at, AttemptedAt: at, Failure: &application.Failure{Code: application.CodeOffline, Message: "offline: link down"}},
		Sessions: []application.SessionProjection{{
			Target: application.SessionTarget{Owner: token.Target, SessionID: "same"}, Token: token,
			Session: application.Session{ID: "same", Name: "Fixture", Tool: "codex", Group: "core", InventoryOnly: true},
		}},
	}}}
	document, err := PresentInventory(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Targets) != 1 || document.Targets[0].TargetID != "connection/lab" || document.Targets[0].Sessions[0].TargetID != "connection/lab/same" || document.Targets[0].Sessions[0].Token == "" {
		t.Fatalf("document = %+v", document)
	}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"scope":"workspace"`, `"state":"stale"`, `"code":"offline"`, `"target_id":"connection/lab/same"`} {
		if !strings.Contains(string(payload), field) {
			t.Fatalf("JSON missing %s: %s", field, payload)
		}
	}
}

func TestDecodeReaderTokenRejectsUnknownFields(t *testing.T) {
	payload := `{"target":{"kind":"device"},"generation":0,"refresh_sequence":0,"environment_id":"env","owner_instance_id":"owner","revision":3,"can_create_bound_fixture":true,"can_rename_fixture":true,"future":true}`
	encoded := "rt1." + rawBase64([]byte(payload))
	if _, err := DecodeReaderToken(encoded); application.CodeOf(err) != application.CodeInvalidRequest {
		t.Fatalf("unknown token field error = %v", err)
	}
}
