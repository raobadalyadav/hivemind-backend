package payments

import (
	"context"
	"errors"

	"github.com/hivemind/backend/pkg/cashfree"
)

// NewCashfreeGateway adapts the Cashfree client to GatewayClient (nil in → nil out, i.e. "not configured").
func NewCashfreeGateway(c *cashfree.Client) GatewayClient {
	if c == nil {
		return nil
	}
	return cashfreeGateway{c}
}

type cashfreeGateway struct{ *cashfree.Client }

func (g cashfreeGateway) OrderPayments(ctx context.Context, orderID string) ([]GatewayPayment, error) {
	list, err := g.Client.OrderPayments(ctx, orderID)
	if err != nil {
		return nil, err
	}
	out := make([]GatewayPayment, 0, len(list))
	for _, p := range list {
		out = append(out, GatewayPayment{ID: p.CFPaymentID, Status: p.Status, AmountMinor: int64(p.Amount*100 + 0.5)})
	}
	return out, nil
}

// AdaptBookings maps the bookings package's "plan is full" error (seatFull) to ErrSeatGone, which is what
// MarkCaptured reacts to by refunding.
func AdaptBookings(b BookingPort, seatFull error) BookingPort { return bookingAdapter{b, seatFull} }

type bookingAdapter struct {
	BookingPort
	seatFull error
}

func (a bookingAdapter) ConfirmPaidBooking(ctx context.Context, bookingID string) error {
	err := a.BookingPort.ConfirmPaidBooking(ctx, bookingID)
	if err != nil && errors.Is(err, a.seatFull) {
		return ErrSeatGone
	}
	return err
}
