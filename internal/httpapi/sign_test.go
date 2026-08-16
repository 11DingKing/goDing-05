package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// responseTimeout bounds how long a single JSON API call may take in tests.
const responseTimeout = 5 * time.Second

// callWithin performs one request and fails the test if no response is produced
// within responseTimeout.
func callWithin(t *testing.T, h http.Handler, method, path string, body any, what string) *httptest.ResponseRecorder {
	t.Helper()
	type outcome struct{ rr *httptest.ResponseRecorder }
	done := make(chan outcome, 1)
	go func() {
		done <- outcome{doRequest(t, h, method, path, body)}
	}()
	select {
	case o := <-done:
		return o.rr
	case <-time.After(responseTimeout):
		t.Fatalf("%s did not return within %s", what, responseTimeout)
		return nil
	}
}

// signableBooking walks a booking through the full workflow up to the point
// where the receipt can be signed, and returns the booking id.
func signableBooking(t *testing.T, h http.Handler, requestID string, base time.Time) string {
	t.Helper()
	sailing := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	eta := time.Now().Add(50 * 24 * time.Hour).UTC().Format(time.RFC3339)

	rr := doRequest(t, h, "POST", "/api/voyages", map[string]any{
		"vessel_name": "MV Sign", "carrier_id": "haijie", "carrier_name": "Haijie Shipping",
		"origin_port": "Ningbo-Zhoushan", "destination_port": "Hamburg",
		"sailing_time": sailing, "eta": eta, "reefer_slots": 3,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create voyage: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	vid := decode(t, rr)["id"].(string)

	rr = doRequest(t, h, "POST", "/api/bookings", map[string]any{
		"request_id": requestID, "voyage_id": vid, "shipper_id": "s1", "shipper_name": "Ningbo Battery Co",
		"cargo_type": "power_battery", "container_count": 1,
		"payment_time": time.Now().UTC().Format(time.RFC3339), "deposit_cents": 100000,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("lock: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	bid := decode(t, rr)["id"].(string)

	rr = doRequest(t, h, "POST", "/api/bookings/"+bid+"/confirm", map[string]any{"power_circuit_id": "circuit-SIGN"})
	if rr.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	for _, step := range []string{"load", "transit"} {
		rr = doRequest(t, h, "POST", "/api/bookings/"+bid+"/"+step, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", step, rr.Code, rr.Body.String())
		}
	}
	for i := 0; i < 3; i++ {
		rr = doRequest(t, h, "POST", "/api/bookings/"+bid+"/temperature", map[string]any{
			"segment":   "transit",
			"timestamp": base.Add(time.Duration(i) * 10 * time.Minute).Format(time.RFC3339),
			"temp_c":    16.0,
		})
		if rr.Code != http.StatusCreated {
			t.Fatalf("record temp %d: expected 201, got %d: %s", i, rr.Code, rr.Body.String())
		}
	}
	rr = doRequest(t, h, "POST", "/api/bookings/"+bid+"/discharge", map[string]any{
		"timestamp": base.Add(30 * time.Minute).Format(time.RFC3339),
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("discharge: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	rr = doRequest(t, h, "POST", "/api/handovers/"+bid+"/complete", map[string]any{
		"timestamp": base.Add(60 * time.Minute).Format(time.RFC3339),
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("handover: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	return bid
}

// TestHTTPSignReceiptReturnsPromptly asserts the sign-off endpoint answers.
func TestHTTPSignReceiptReturnsPromptly(t *testing.T) {
	h, _ := setupHandler(t)
	base := time.Date(2026, 12, 20, 6, 0, 0, 0, time.UTC)
	bid := signableBooking(t, h, "sign-prompt", base)

	rr := callWithin(t, h, "POST", "/api/bookings/"+bid+"/sign", map[string]any{
		"timestamp": base.Add(90 * time.Minute).Format(time.RFC3339),
	}, "sign receipt")
	if rr.Code != http.StatusOK {
		t.Fatalf("sign: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decode(t, rr)
	if resp["has_damage"] != false {
		t.Fatalf("expected no damage for in-range temperatures, got %v", resp["has_damage"])
	}
	if resp["booking_id"] != bid {
		t.Fatalf("expected the report to name booking %s, got %v", bid, resp["booking_id"])
	}
}

// TestHTTPServiceStaysUsableAfterSign asserts the rest of the API keeps working
// once a receipt has been signed.
func TestHTTPServiceStaysUsableAfterSign(t *testing.T) {
	h, _ := setupHandler(t)
	base := time.Date(2026, 12, 20, 6, 0, 0, 0, time.UTC)
	bid := signableBooking(t, h, "sign-then-read", base)

	rr := callWithin(t, h, "POST", "/api/bookings/"+bid+"/sign", map[string]any{
		"timestamp": base.Add(90 * time.Minute).Format(time.RFC3339),
	}, "sign receipt")
	if rr.Code != http.StatusOK {
		t.Fatalf("sign: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	rr = callWithin(t, h, "GET", "/api/voyages", nil, "list voyages after sign")
	if rr.Code != http.StatusOK {
		t.Fatalf("list voyages: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	rr = callWithin(t, h, "GET", "/api/bookings/"+bid, nil, "get booking after sign")
	if rr.Code != http.StatusOK {
		t.Fatalf("get booking: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if decode(t, rr)["status"] != "signed" {
		t.Fatalf("expected the booking to read back as signed, got %v", decode(t, rr)["status"])
	}
	rr = callWithin(t, h, "GET", "/health", nil, "health check after sign")
	if rr.Code != http.StatusOK {
		t.Fatalf("health: expected 200, got %d", rr.Code)
	}
}

// TestHTTPSignReceiptWithBreachReturnsLiability asserts sign-off also answers
// when the temperature record contains a breach.
func TestHTTPSignReceiptWithBreachReturnsLiability(t *testing.T) {
	h, _ := setupHandler(t)
	base := time.Date(2026, 12, 21, 6, 0, 0, 0, time.UTC)
	bid := signableBooking(t, h, "sign-breach", base)

	for i := 0; i < 3; i++ {
		rr := doRequest(t, h, "POST", "/api/bookings/"+bid+"/temperature", map[string]any{
			"segment":   "handover",
			"timestamp": base.Add(time.Duration(60+i*10) * time.Minute).Format(time.RFC3339),
			"temp_c":    29.0,
		})
		if rr.Code != http.StatusCreated {
			t.Fatalf("record breach %d: expected 201, got %d: %s", i, rr.Code, rr.Body.String())
		}
	}

	rr := callWithin(t, h, "POST", "/api/bookings/"+bid+"/sign", map[string]any{
		"timestamp": base.Add(120 * time.Minute).Format(time.RFC3339),
	}, "sign receipt with breach")
	if rr.Code != http.StatusOK {
		t.Fatalf("sign: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decode(t, rr)
	if resp["has_damage"] != true {
		t.Fatalf("expected damage to be reported, got %v", resp["has_damage"])
	}
	segments, ok := resp["segments"].([]any)
	if !ok || len(segments) != 1 {
		t.Fatalf("expected 1 damaged segment, got %v", resp["segments"])
	}
	seg := segments[0].(map[string]any)
	if seg["segment"] != "handover" || seg["responsible_party"] != "temp_service" {
		t.Fatalf("expected the handover leg attributed to temp_service, got %v", seg)
	}
}
