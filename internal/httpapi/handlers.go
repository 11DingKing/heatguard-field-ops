package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/operations"
	"github.com/11DingKing/heatguard-field-ops/internal/planning"
	"github.com/11DingKing/heatguard-field-ops/internal/requestid"
)

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}
func (s *Server) readiness(w http.ResponseWriter, r *http.Request) {
	if err := s.ready.Ping(r.Context()); err != nil {
		writeError(w, r, domain.ErrUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.auth.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Logout(r.Context(), bearer(r)); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) createRoute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string                `json:"name"`
		Zone     string                `json:"zone"`
		Kind     domain.ActivityKind   `json:"activity_kind"`
		Distance int                   `json:"distance_meters"`
		Segments []domain.RouteSegment `json:"segments"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	u := principal(r.Context())
	route, segments, err := s.planning.CreateRoute(r.Context(), planning.CreateRouteInput{Name: in.Name, Zone: in.Zone, Kind: in.Kind, DistanceMeters: in.Distance, Segments: in.Segments, ActorID: u.ID, RequestID: requestid.From(r.Context())})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"route": route, "segments": segments})
}

func (s *Server) createLeader(w http.ResponseWriter, r *http.Request) {
	var in domain.Leader
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.planning.RegisterLeader(r.Context(), in, principal(r.Context()).ID, requestid.From(r.Context()))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) createParticipant(w http.ResponseWriter, r *http.Request) {
	var in domain.Participant
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.planning.RegisterParticipant(r.Context(), in, principal(r.Context()).ID, requestid.From(r.Context()))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) createRestriction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in domain.HealthRestriction
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	in.ParticipantID = id
	out, err := s.planning.AddRestriction(r.Context(), in, principal(r.Context()).ID, requestid.From(r.Context()))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) createRiskRule(w http.ResponseWriter, r *http.Request) {
	var in domain.RiskRule
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.planning.AddRiskRule(r.Context(), in, principal(r.Context()).ID, requestid.From(r.Context()))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
func (s *Server) createWave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RouteID  int64     `json:"route_id"`
		LeaderID int64     `json:"leader_id"`
		Name     string    `json:"name"`
		Start    time.Time `json:"scheduled_start"`
		End      time.Time `json:"expected_end"`
		Capacity int       `json:"capacity"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	wave, err := s.planning.CreateWave(r.Context(), planning.CreateWaveInput{RouteID: in.RouteID, LeaderID: in.LeaderID, ActorID: principal(r.Context()).ID, Name: in.Name, Start: in.Start, End: in.End, Capacity: in.Capacity, RequestID: requestid.From(r.Context())})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, wave)
}
func (s *Server) getWave(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	wave, members, err := s.planning.GetWave(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"wave": wave, "participants": members})
}
func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		ParticipantID int64 `json:"participant_id"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.planning.Enroll(r.Context(), id, in.ParticipantID, principal(r.Context()).ID, requestid.From(r.Context()))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
func (s *Server) readyWave(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	version, err := decodeVersion(w, r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.planning.MarkReady(r.Context(), id, version, principal(r.Context()).ID, requestid.From(r.Context())); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) depart(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Version     int64   `json:"version"`
		Temperature float64 `json:"temperature"`
		HeatIndex   float64 `json:"heat_index"`
		Key         string  `json:"idempotency_key"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.operations.Depart(r.Context(), operations.DepartInput{WaveID: id, Version: in.Version, ActorID: principal(r.Context()).ID, Temperature: in.Temperature, HeatIndex: in.HeatIndex, IdempotencyKey: in.Key, RequestID: requestid.From(r.Context())})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) recordEvent(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		ParticipantID int64                 `json:"participant_id"`
		SegmentID     int64                 `json:"segment_id"`
		Type          domain.FieldEventType `json:"type"`
		OccurredAt    time.Time             `json:"occurred_at"`
		Key           string                `json:"idempotency_key"`
		Details       string                `json:"details"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.operations.RecordEvent(r.Context(), operations.RecordEventInput{WaveID: id, ParticipantID: in.ParticipantID, SegmentID: in.SegmentID, ActorID: principal(r.Context()).ID, Type: in.Type, OccurredAt: in.OccurredAt, IdempotencyKey: in.Key, Details: in.Details, RequestID: requestid.From(r.Context())})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
func (s *Server) closeWave(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	v, err := decodeVersion(w, r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.operations.CloseWave(r.Context(), id, v, principal(r.Context()).ID, requestid.From(r.Context())); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) ackAlert(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	v, err := decodeVersion(w, r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.operations.AcknowledgeAlert(r.Context(), id, v, principal(r.Context()).ID, requestid.From(r.Context())); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.Validation("id", "must be positive")
	}
	return id, nil
}
func decodeVersion(w http.ResponseWriter, r *http.Request) (int64, error) {
	var in struct {
		Version int64 `json:"version"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return 0, err
	}
	if in.Version <= 0 {
		return 0, domain.Validation("version", "must be positive")
	}
	return in.Version, nil
}
