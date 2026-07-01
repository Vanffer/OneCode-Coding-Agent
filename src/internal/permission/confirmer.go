package permission

import "context"

// Confirmer asks a user or test double to resolve an ambiguous permission request.
type Confirmer interface {
	Confirm(ctx context.Context, req ConfirmationRequest) (ConfirmationResponse, error)
}

// StaticConfirmer returns the same choice for every request.
type StaticConfirmer struct {
	Choice ConfirmationChoice
	Count  int
}

func (c *StaticConfirmer) Confirm(ctx context.Context, req ConfirmationRequest) (ConfirmationResponse, error) {
	select {
	case <-ctx.Done():
		return ConfirmationResponse{}, ctx.Err()
	default:
	}
	c.Count++
	choice := c.Choice
	if choice == "" {
		choice = ChoiceDeny
	}
	return ConfirmationResponse{RequestID: req.ID, Choice: choice}, nil
}
