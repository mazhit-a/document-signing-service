package notify

import (
	"context"
	"log/slog"
)

type SigningRequest struct {
	DocumentID    string
	DocumentTitle string
	SignerID      string
	SignerName    string
	SignerEmail   string
	Order         int
	URL           string
}

type Notifier interface {
	SigningRequested(ctx context.Context, req SigningRequest) error
}

type LogNotifier struct {
	logger *slog.Logger
}

func NewLogNotifier(logger *slog.Logger) *LogNotifier {
	return &LogNotifier{logger: logger}
}

func (n *LogNotifier) SigningRequested(ctx context.Context, req SigningRequest) error {
	n.logger.InfoContext(ctx, "signing request issued",
		"document_id", req.DocumentID,
		"signer_id", req.SignerID,
		"signer_order", req.Order)
	return nil
}

type Noop struct{}

func (Noop) SigningRequested(context.Context, SigningRequest) error { return nil }
