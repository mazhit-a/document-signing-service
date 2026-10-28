package http

import (
	"context"
	"net/http"

	"github.com/mazhit-a/mini-kedo/internal/service"
)

type VerifyUseCase interface {
	Verify(ctx context.Context, ownerID, id string) (service.VerifyReport, error)
}

type VerifyHandlers struct {
	uc VerifyUseCase
}

func NewVerifyHandlers(uc VerifyUseCase) *VerifyHandlers {
	return &VerifyHandlers{uc: uc}
}

type fileCheckResponse struct {
	OK             bool   `json:"ok"`
	ExpectedSHA256 string `json:"expected_sha256"`
	ActualSHA256   string `json:"actual_sha256"`
}

type signatureCheckResponse struct {
	SignerID            string `json:"signer_id"`
	Order               int    `json:"order"`
	SealOK              bool   `json:"seal_ok"`
	HashMatchesDocument bool   `json:"hash_matches_document"`
}

type chainCheckResponse struct {
	OK                 bool   `json:"ok"`
	Events             int    `json:"events"`
	FirstBrokenEventID *int64 `json:"first_broken_event_id"`
}

type verifyResponse struct {
	Valid      bool                     `json:"valid"`
	CheckedAt  string                   `json:"checked_at"`
	File       fileCheckResponse        `json:"file"`
	Signatures []signatureCheckResponse `json:"signatures"`
	AuditChain chainCheckResponse       `json:"audit_chain"`
}

func (h *VerifyHandlers) Verify(w http.ResponseWriter, r *http.Request) {
	rep, err := h.uc.Verify(r.Context(), UserIDFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	resp := verifyResponse{
		Valid:     rep.Valid,
		CheckedAt: formatTime(rep.CheckedAt),
		File:      fileCheckResponse{OK: rep.File.OK, ExpectedSHA256: rep.File.ExpectedSHA256, ActualSHA256: rep.File.ActualSHA256},
		AuditChain: chainCheckResponse{
			OK: rep.AuditChain.OK, Events: rep.AuditChain.Events, FirstBrokenEventID: rep.AuditChain.FirstBrokenEventID,
		},
		Signatures: make([]signatureCheckResponse, len(rep.Signatures)),
	}
	for i, s := range rep.Signatures {
		resp.Signatures[i] = signatureCheckResponse{SignerID: s.SignerID, Order: s.Order, SealOK: s.SealOK, HashMatchesDocument: s.HashMatchesDocument}
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, resp)
}
