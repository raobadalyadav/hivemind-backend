package subscriptions

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

func TestSubscribeIsClosedUntilReceiptsAreVerified(t *testing.T) {
	_, err := (&Handler{}).Subscribe(context.Background(), &socialv1.SubscribeRequest{ProductId: "pro", StoreReceipt: "anything"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("an unverified receipt must never grant a subscription, got %v", err)
	}
}
