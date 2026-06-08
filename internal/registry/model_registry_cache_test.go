package registry

import "testing"

func TestGetAvailableModelsReturnsClonedSnapshots(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("client-1", "OpenAI", []*ModelInfo{{ID: "m1", OwnedBy: "team-a", DisplayName: "Model One"}})

	first := r.GetAvailableModels("openai")
	if len(first) != 1 {
		t.Fatalf("expected 1 model, got %d", len(first))
	}
	first[0]["id"] = "mutated"
	first[0]["display_name"] = "Mutated"

	second := r.GetAvailableModels("openai")
	if got := second[0]["id"]; got != "m1" {
		t.Fatalf("expected cached snapshot to stay isolated, got id %v", got)
	}
	if got := second[0]["display_name"]; got != "Model One" {
		t.Fatalf("expected cached snapshot to stay isolated, got display_name %v", got)
	}
}

func TestGetAvailableModelsInvalidatesCacheOnRegistryChanges(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("client-1", "OpenAI", []*ModelInfo{{ID: "m1", OwnedBy: "team-a", DisplayName: "Model One"}})

	models := r.GetAvailableModels("openai")
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if got := models[0]["display_name"]; got != "Model One" {
		t.Fatalf("expected initial display_name Model One, got %v", got)
	}

	r.RegisterClient("client-1", "OpenAI", []*ModelInfo{{ID: "m1", OwnedBy: "team-a", DisplayName: "Model One Updated"}})
	models = r.GetAvailableModels("openai")
	if got := models[0]["display_name"]; got != "Model One Updated" {
		t.Fatalf("expected updated display_name after cache invalidation, got %v", got)
	}

	r.SuspendClientModel("client-1", "m1", "manual")
	models = r.GetAvailableModels("openai")
	if len(models) != 0 {
		t.Fatalf("expected no available models after suspension, got %d", len(models))
	}

	r.ResumeClientModel("client-1", "m1")
	models = r.GetAvailableModels("openai")
	if len(models) != 1 {
		t.Fatalf("expected model to reappear after resume, got %d", len(models))
	}
}

func TestGetAvailableModelsForClientsReturnsClonedSnapshots(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("client-1", "OpenAI", []*ModelInfo{{ID: "m1", OwnedBy: "team-a", DisplayName: "Model One"}})

	first := r.GetAvailableModelsForClients("openai", []string{"client-1"})
	if len(first) != 1 {
		t.Fatalf("expected 1 model, got %d", len(first))
	}
	first[0]["id"] = "mutated"
	first[0]["display_name"] = "Mutated"

	second := r.GetAvailableModelsForClients("openai", []string{"client-1"})
	if got := second[0]["id"]; got != "m1" {
		t.Fatalf("expected cached scoped snapshot to stay isolated, got id %v", got)
	}
	if got := second[0]["display_name"]; got != "Model One" {
		t.Fatalf("expected cached scoped snapshot to stay isolated, got display_name %v", got)
	}
}

func TestGetAvailableModelsForClientCacheUsesPrecomputedScope(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("client-1", "OpenAI", []*ModelInfo{{ID: "m1", OwnedBy: "team-a", DisplayName: "Model One"}})
	r.RegisterClient("client-2", "OpenAI", []*ModelInfo{{ID: "m2", OwnedBy: "team-b", DisplayName: "Model Two"}})

	models := r.GetAvailableModelsForClientCache("openai", "client-1", []string{"client-1"})
	if len(models) != 1 {
		t.Fatalf("expected one scoped model, got %d", len(models))
	}
	if got := models[0]["id"]; got != "m1" {
		t.Fatalf("expected scoped model m1, got %v", got)
	}

	models[0]["id"] = "mutated"
	models = r.GetAvailableModelsForClientCache("openai", "client-1", []string{"client-1"})
	if got := models[0]["id"]; got != "m1" {
		t.Fatalf("expected cached scoped model clone m1, got %v", got)
	}
}

func TestGetAvailableModelsForClientCacheSnapshotTracksVersionAndExpiry(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("client-1", "OpenAI", []*ModelInfo{{ID: "m1", OwnedBy: "team-a", DisplayName: "Model One"}})

	models, expiresAt, version := r.GetAvailableModelsForClientCacheSnapshot("openai", "client-1", []string{"client-1"})
	if len(models) != 1 {
		t.Fatalf("expected one scoped model, got %d", len(models))
	}
	if !expiresAt.IsZero() {
		t.Fatalf("expected initial snapshot not to expire, got %s", expiresAt)
	}
	if version == 0 {
		t.Fatal("expected non-zero cache version")
	}

	r.RegisterClient("client-1", "OpenAI", []*ModelInfo{{ID: "m1", OwnedBy: "team-a", DisplayName: "Model One Updated"}})
	models, expiresAt, updatedVersion := r.GetAvailableModelsForClientCacheSnapshot("openai", "client-1", []string{"client-1"})
	if len(models) != 1 {
		t.Fatalf("expected one updated scoped model, got %d", len(models))
	}
	if got := models[0]["display_name"]; got != "Model One Updated" {
		t.Fatalf("expected updated display_name, got %v", got)
	}
	if !expiresAt.IsZero() {
		t.Fatalf("expected updated snapshot not to expire, got %s", expiresAt)
	}
	if updatedVersion <= version {
		t.Fatalf("expected cache version to increase after registry change, got %d then %d", version, updatedVersion)
	}

	r.SetModelQuotaExceeded("client-1", "m1")
	models, expiresAt, quotaVersion := r.GetAvailableModelsForClientCacheSnapshot("openai", "client-1", []string{"client-1"})
	if len(models) != 1 {
		t.Fatalf("expected quota-cooling model to remain listed, got %d", len(models))
	}
	if expiresAt.IsZero() {
		t.Fatal("expected quota snapshot to expire at recovery time")
	}
	if quotaVersion <= updatedVersion {
		t.Fatalf("expected cache version to increase after quota change, got %d then %d", updatedVersion, quotaVersion)
	}
}

func TestGetAvailableModelsForClientsInvalidatesCacheOnRegistryChanges(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("client-1", "OpenAI", []*ModelInfo{{ID: "m1", OwnedBy: "team-a", DisplayName: "Model One"}})

	models := r.GetAvailableModelsForClients("openai", []string{"client-1"})
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if got := models[0]["display_name"]; got != "Model One" {
		t.Fatalf("expected initial display_name Model One, got %v", got)
	}

	r.RegisterClient("client-1", "OpenAI", []*ModelInfo{{ID: "m1", OwnedBy: "team-a", DisplayName: "Model One Updated"}})
	models = r.GetAvailableModelsForClients("openai", []string{"client-1"})
	if got := models[0]["display_name"]; got != "Model One Updated" {
		t.Fatalf("expected updated display_name after scoped cache invalidation, got %v", got)
	}

	r.SuspendClientModel("client-1", "m1", "manual")
	models = r.GetAvailableModelsForClients("openai", []string{"client-1"})
	if len(models) != 0 {
		t.Fatalf("expected no available scoped models after suspension, got %d", len(models))
	}

	r.ResumeClientModel("client-1", "m1")
	models = r.GetAvailableModelsForClients("openai", []string{"client-1"})
	if len(models) != 1 {
		t.Fatalf("expected scoped model to reappear after resume, got %d", len(models))
	}
}
