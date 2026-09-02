package presence

import (
	"testing"
	"time"

	"drs/backend/internal/models"
)

func meta() DeviceMeta {
	return DeviceMeta{OrgID: "org-1", Type: models.DeviceTypeWindows, IPAddress: "192.168.1.50"}
}

func TestPresenceStore_OnlineOffline(t *testing.T) {
	store := NewMemoryStore()
	devID := "device-123"

	if store.IsDeviceOnline(devID) {
		t.Fatalf("device should not be online initially")
	}

	p := store.SetDeviceOnline(devID, meta())
	if p.Status != models.DeviceStatusOnline {
		t.Fatalf("expected status online, got %s", p.Status)
	}
	if !store.IsDeviceOnline(devID) {
		t.Fatalf("device should be online after SetDeviceOnline")
	}
	if got := len(store.GetAllOnlineDevices()); got != 1 {
		t.Fatalf("expected 1 online device, got %d", got)
	}

	store.SetDeviceOffline(devID)
	if store.IsDeviceOnline(devID) {
		t.Fatalf("device should be offline after SetDeviceOffline")
	}
}

func TestSessionRegistry_SingleSessionPerDevice(t *testing.T) {
	store := NewMemoryStore()
	devID := "device-456"
	store.SetDeviceOnline(devID, meta())

	sess1, err := store.RegisterSession("sess-1", devID, "admin-1", models.SessionModeView)
	if err != nil {
		t.Fatalf("failed to register first session: %v", err)
	}
	if sess1.SessionID != "sess-1" {
		t.Fatalf("session id mismatch")
	}

	pres, ok := store.GetDevicePresence(devID)
	if !ok || pres.Status != models.DeviceStatusInSession {
		t.Fatalf("device status should be in_session, got %v", pres)
	}

	// SRS FR-5.3: a second viewer must be refused, not silently share the device.
	if _, err = store.RegisterSession("sess-2", devID, "admin-2", models.SessionModeView); err != ErrDeviceAlreadyInSession {
		t.Fatalf("expected ErrDeviceAlreadyInSession, got: %v", err)
	}

	if endedSess, ok := store.EndSession("sess-1"); !ok || endedSess.SessionID != "sess-1" {
		t.Fatalf("failed to end session")
	}

	if _, err = store.RegisterSession("sess-3", devID, "admin-2", models.SessionModeView); err != nil {
		t.Fatalf("failed to register session after ending previous: %v", err)
	}
}

func TestPresenceSubscription(t *testing.T) {
	store := NewMemoryStore()
	adminID := "admin-sub-1"

	ch := store.Subscribe(adminID)
	defer store.Unsubscribe(adminID, ch)

	store.SetDeviceOnline("dev-sub-1", DeviceMeta{OrgID: "org-1", Type: models.DeviceTypeAndroid, IPAddress: "10.0.0.1"})

	select {
	case event := <-ch:
		if event.DeviceID != "dev-sub-1" || event.Type != "device_online" {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for presence event")
	}
}

// TestPresenceEventOrdering pins down the bug that made a device latch as offline
// after it had already come back: events used to be published from a new goroutine per
// change, so two transitions could reach a subscriber in the wrong order.
func TestPresenceEventOrdering(t *testing.T) {
	store := NewMemoryStore()
	ch := store.Subscribe("admin-order")
	defer store.Unsubscribe("admin-order", ch)

	store.SetDeviceOnline("dev-order", meta())
	store.SetDeviceOffline("dev-order")
	store.SetDeviceOnline("dev-order", meta())

	want := []string{"device_online", "device_offline", "device_online"}
	for i, expected := range want {
		select {
		case event := <-ch:
			if event.Type != expected {
				t.Fatalf("event %d: expected %s, got %s", i, expected, event.Type)
			}
		case <-time.After(time.Second):
			t.Fatalf("event %d (%s) never arrived", i, expected)
		}
	}
}

// TestPresenceEventCarriesRBACFields matters because /ws/presence filters on these
// without a database round trip; if they are empty every Admin sees every device.
func TestPresenceEventCarriesRBACFields(t *testing.T) {
	store := NewMemoryStore()
	ch := store.Subscribe("admin-rbac")
	defer store.Unsubscribe("admin-rbac", ch)

	adminID := "admin-9"
	store.SetDeviceOnline("dev-rbac", DeviceMeta{
		OrgID:           "org-7",
		Type:            models.DeviceTypeWindows,
		AssignedAdminID: &adminID,
	})

	select {
	case event := <-ch:
		if event.OrgID != "org-7" {
			t.Fatalf("expected org-7, got %q", event.OrgID)
		}
		if event.AssignedAdminID == nil || *event.AssignedAdminID != adminID {
			t.Fatalf("expected assigned admin %q, got %v", adminID, event.AssignedAdminID)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for presence event")
	}
}
