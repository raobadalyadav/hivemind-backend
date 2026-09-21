package payments

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/internal/bookings"
)

type fakeGateway struct {
	createdAmounts []int64
	refunds        []int64
	refundErr      error
	payments       []GatewayPayment
	onlyOrder      string // when set, only this order has payments (other tests' leftovers stay unpaid)
}

func (g *fakeGateway) CreateOrder(_ context.Context, _ string, amount int64, _, _, _, _ string) (string, string, error) {
	g.createdAmounts = append(g.createdAmounts, amount)
	return "cf_order", "session_123", nil
}
func (g *fakeGateway) Refund(_ context.Context, _, _ string, amount int64, _ string) (string, error) {
	if g.refundErr != nil {
		return "", g.refundErr
	}
	g.refunds = append(g.refunds, amount)
	return "cf_refund", nil
}
func (g *fakeGateway) OrderPayments(_ context.Context, id string) ([]GatewayPayment, error) {
	if g.onlyOrder != "" && g.onlyOrder != id {
		return nil, nil
	}
	return g.payments, nil
}

type moneyEnv struct {
	pool             *pgxpool.Pool
	repo             *Repository
	bk               *bookings.Repository
	svc              *Service
	gw               *fakeGateway
	host, guest, oth string
	plan             string
}

func newMoneyEnv(t *testing.T, capacity int) *moneyEnv {
	t.Helper()
	pool := testPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	sfx := time.Now().Format("150405.000000000")
	e := &moneyEnv{pool: pool, repo: NewRepository(pool), bk: bookings.NewRepository(pool), gw: &fakeGateway{}}
	mk := func(l string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "mny-"+l+"-"+sfx+"@example.com").Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	e.host, e.guest, e.oth = mk("h"), mk("g"), mk("o")
	if err := pool.QueryRow(ctx, `INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, price_minor, currency, status)
		VALUES ('Paid', $1, now()+interval '3 days', now()+interval '3 days 2 hours', $2, 50000, 'INR', 'published') RETURNING id`, e.host, capacity).Scan(&e.plan); err != nil {
		t.Fatal(err)
	}
	e.svc = NewService(e.repo, e.gw, AdaptBookings(bookings.NewService(e.bk, nil), bookings.ErrPlanFull), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return e
}

func (e *moneyEnv) book(t *testing.T, user string) *bookings.Booking {
	t.Helper()
	b, err := e.bk.Create(context.Background(), &bookings.Booking{PlanID: e.plan, UserID: user})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (e *moneyEnv) status(t *testing.T, bookingID string) string {
	t.Helper()
	b, err := e.bk.Get(context.Background(), bookingID)
	if err != nil {
		t.Fatal(err)
	}
	return b.Status
}

func TestCreateOrder_TheServerDecidesTheAmount(t *testing.T) {
	e := newMoneyEnv(t, 5)
	ctx := context.Background()
	b := e.book(t, e.guest)

	// even if the client "sent" a tiny amount, the order is price + service fee (₹500 + 5% = ₹525)
	order, session, err := e.svc.CreateOrder(ctx, &Order{BookingID: b.ID, AmountMinor: 100}, e.guest, "9999999999", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if order.AmountMinor != 52500 || len(e.gw.createdAmounts) != 1 || e.gw.createdAmounts[0] != 52500 || session != "session_123" {
		t.Fatalf("amount must be server-computed: order=%d gateway=%v session=%q", order.AmountMinor, e.gw.createdAmounts, session)
	}
	if _, _, err := e.svc.CreateOrder(ctx, &Order{BookingID: b.ID}, e.oth, "", "", false); err != ErrForbidden {
		t.Fatalf("someone else can't pay for my booking: %v", err)
	}
	// still not confirmed until the payment is captured
	if e.status(t, b.ID) != "payment_pending" {
		t.Fatal("booking waits for the payment")
	}
}

func TestPayment_ConfirmsTheBooking_ByWebhookOrVerify(t *testing.T) {
	e := newMoneyEnv(t, 5)
	ctx := context.Background()

	// webhook path
	b := e.book(t, e.guest)
	order, _, err := e.svc.CreateOrder(ctx, &Order{BookingID: b.ID}, e.guest, "9999999999", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkCaptured(ctx, order.ID, "pay_"+order.ID, order.AmountMinor); err != nil {
		t.Fatalf("webhook capture: %v", err)
	}
	if e.status(t, b.ID) != "confirmed" {
		t.Fatalf("paid booking is confirmed, got %s", e.status(t, b.ID))
	}
	// a replayed webhook changes nothing
	if _, err := e.svc.MarkCaptured(ctx, order.ID, "pay_"+order.ID, order.AmountMinor); err != nil {
		t.Fatalf("replay: %v", err)
	}

	// verify path (webhook never arrives): the gateway says SUCCESS
	b2 := e.book(t, e.oth)
	order2, _, err := e.svc.CreateOrder(ctx, &Order{BookingID: b2.ID}, e.oth, "9999999999", "", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.VerifyOrder(ctx, order2.ID, e.oth)
	if err != nil || got.Status == "paid" || e.status(t, b2.ID) != "payment_pending" {
		t.Fatalf("nothing paid yet: %+v %v", got, err)
	}
	e.gw.payments = []GatewayPayment{{ID: "cf_pay_2_" + order2.ID, Status: "SUCCESS", AmountMinor: order2.AmountMinor}}
	got, err = e.svc.VerifyOrder(ctx, order2.ID, e.oth)
	if err != nil || got.Status != "paid" || e.status(t, b2.ID) != "confirmed" {
		t.Fatalf("verify settles the order and confirms the booking: %+v %v", got, err)
	}
	if _, err := e.svc.VerifyOrder(ctx, order2.ID, e.guest); err != ErrPaymentNotFound {
		t.Fatalf("other people can't verify (or probe) my order: %v", err)
	}
}

func TestPayment_ForALostSeatIsRefundedInFull(t *testing.T) {
	e := newMoneyEnv(t, 1)
	ctx := context.Background()
	slow := e.book(t, e.guest)
	order, _, _ := e.svc.CreateOrder(ctx, &Order{BookingID: slow.ID}, e.guest, "9999999999", "", false)
	// the hold lapses and someone else pays for the only seat
	e.pool.Exec(ctx, `UPDATE bookings SET expires_at = now() - interval '1 minute' WHERE id = $1`, slow.ID)
	bookings.NewService(e.bk, nil).ExpirePendingBookings(ctx, time.Now())
	other := e.book(t, e.oth)
	oo, _, _ := e.svc.CreateOrder(ctx, &Order{BookingID: other.ID}, e.oth, "9999999999", "", false)
	if _, err := e.svc.MarkCaptured(ctx, oo.ID, "pay_other_"+oo.ID, oo.AmountMinor); err != nil {
		t.Fatal(err)
	}
	// now the slow payer's money lands
	_, err := e.svc.MarkCaptured(ctx, order.ID, "pay_slow_"+order.ID, order.AmountMinor)
	if !errors.Is(err, ErrPlanFullAfterPay) {
		t.Fatalf("a payment for a lost seat reports it: %v", err)
	}
	if len(e.gw.refunds) != 1 || e.gw.refunds[0] != order.AmountMinor {
		t.Fatalf("the whole payment is refunded: %v", e.gw.refunds)
	}
	if e.status(t, other.ID) != "confirmed" {
		t.Fatal("the seat stays with the person who got it")
	}
}

func TestRefund_FollowsThePolicyAndNeverExceedsWhatWasPaid(t *testing.T) {
	e := newMoneyEnv(t, 5)
	ctx := context.Background()
	b := e.book(t, e.guest)
	order, _, _ := e.svc.CreateOrder(ctx, &Order{BookingID: b.ID}, e.guest, "9999999999", "", false)
	pay, err := e.svc.MarkCaptured(ctx, order.ID, "pay_r_"+order.ID, order.AmountMinor)
	if err != nil {
		t.Fatal(err)
	}

	if err := e.svc.RefundBookingIfCaptured(ctx, b.ID, "late cancel", 0); err != nil || len(e.gw.refunds) != 0 {
		t.Fatalf("0%% means nothing goes back: %v %v", err, e.gw.refunds)
	}
	if err := e.svc.RefundBookingIfCaptured(ctx, b.ID, "half", 50); err != nil || len(e.gw.refunds) != 1 || e.gw.refunds[0] != pay.AmountMinor/2 {
		t.Fatalf("50%%: %v %v", err, e.gw.refunds)
	}
	// asking for 100% afterwards only returns the rest
	if err := e.svc.RefundBookingIfCaptured(ctx, b.ID, "all", 100); err != nil || len(e.gw.refunds) != 2 || e.gw.refunds[0]+e.gw.refunds[1] != pay.AmountMinor {
		t.Fatalf("the remainder only: %v %v", err, e.gw.refunds)
	}
	// and doing it again is a no-op (the worker may redeliver the event)
	if err := e.svc.RefundBookingIfCaptured(ctx, b.ID, "again", 100); err != nil || len(e.gw.refunds) != 2 {
		t.Fatalf("idempotent: %v %v", err, e.gw.refunds)
	}
	if _, err := e.svc.RefundPayment(ctx, pay.ID, 1, "too much"); err != ErrPaymentNotRefundable {
		t.Fatalf("can't refund more than was paid: %v", err)
	}
}

func TestRefund_GatewayFailureIsRetriableWithoutDoubleRefunding(t *testing.T) {
	e := newMoneyEnv(t, 5)
	ctx := context.Background()
	b := e.book(t, e.guest)
	order, _, _ := e.svc.CreateOrder(ctx, &Order{BookingID: b.ID}, e.guest, "9999999999", "", false)
	e.svc.MarkCaptured(ctx, order.ID, "pay_f_"+order.ID, order.AmountMinor)

	e.gw.refundErr = errors.New("gateway down")
	if err := e.svc.RefundBookingIfCaptured(ctx, b.ID, "cancel", 100); err == nil {
		t.Fatal("a gateway failure is reported so the worker retries")
	}
	var failed int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM refunds r JOIN payments p ON p.id=r.payment_id JOIN orders o ON o.id=p.order_id WHERE o.booking_id=$1 AND r.status='failed'`, b.ID).Scan(&failed)
	if failed != 1 {
		t.Fatalf("the failed attempt is recorded: %d", failed)
	}
	e.gw.refundErr = nil
	if err := e.svc.RefundBookingIfCaptured(ctx, b.ID, "cancel", 100); err != nil || len(e.gw.refunds) != 1 || e.gw.refunds[0] != order.AmountMinor {
		t.Fatalf("the retry refunds exactly once, in full: %v %v", err, e.gw.refunds)
	}
}

func TestCreateOrder_CreditsCoverEverything_ConfirmsWithoutTheGateway(t *testing.T) {
	e := newMoneyEnv(t, 5)
	ctx := context.Background()
	e.pool.Exec(ctx, `INSERT INTO credit_ledger (user_id, amount_minor, reason) VALUES ($1, 100000, 'test grant')`, e.guest)
	b := e.book(t, e.guest)
	order, session, err := e.svc.CreateOrder(ctx, &Order{BookingID: b.ID}, e.guest, "", "", true)
	if err != nil || order.AmountMinor != 0 || session != "" || order.Status != "paid" {
		t.Fatalf("fully covered order: %+v %q %v", order, session, err)
	}
	if e.status(t, b.ID) != "confirmed" || len(e.gw.createdAmounts) != 0 {
		t.Fatalf("confirmed without a gateway call: %s %v", e.status(t, b.ID), e.gw.createdAmounts)
	}
	if bal, _ := e.repo.GetCreditBalance(ctx, e.guest); bal != 100000-52500 {
		t.Fatalf("credits spent = the total owed, balance %d", bal)
	}
}

func TestAbandonedOrders_GiveCreditsBack(t *testing.T) {
	e := newMoneyEnv(t, 5)
	ctx := context.Background()
	e.pool.Exec(ctx, `INSERT INTO credit_ledger (user_id, amount_minor, reason) VALUES ($1, 10000, 'test grant')`, e.guest)
	b := e.book(t, e.guest)
	if _, _, err := e.svc.CreateOrder(ctx, &Order{BookingID: b.ID}, e.guest, "9999999999", "", true); err != nil {
		t.Fatal(err)
	}
	if bal, _ := e.repo.GetCreditBalance(ctx, e.guest); bal != 0 {
		t.Fatalf("credits are held while paying: %d", bal)
	}
	// they never pay: the hold expires and the credits come back
	e.pool.Exec(ctx, `UPDATE bookings SET expires_at = now() - interval '1 minute' WHERE id = $1`, b.ID)
	bookings.NewService(e.bk, nil).ExpirePendingBookings(ctx, time.Now())
	if _, err := e.svc.ReleaseAbandonedOrders(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if bal, _ := e.repo.GetCreditBalance(ctx, e.guest); bal != 10000 {
		t.Fatalf("credits returned after abandonment: %d", bal)
	}
}

func TestReceipt_IssuedOnceWithTheGSTSplit(t *testing.T) {
	e := newMoneyEnv(t, 5)
	e.svc.WithSeller(Seller{Name: "HiveMind Pvt Ltd", GSTIN: "29ABCDE1234F1Z5", Address: "Bengaluru", FeeGSTPercent: 18})
	ctx := context.Background()
	b := e.book(t, e.guest)
	order, _, _ := e.svc.CreateOrder(ctx, &Order{BookingID: b.ID}, e.guest, "9999999999", "", false)

	if _, err := e.svc.GetReceipt(ctx, b.ID, e.guest); err != ErrPaymentNotFound {
		t.Fatalf("no receipt before a payment exists: %v", err)
	}
	e.svc.MarkCaptured(ctx, order.ID, "pay_rc_"+order.ID, order.AmountMinor)

	r, err := e.svc.GetReceipt(ctx, b.ID, e.guest)
	if err != nil {
		t.Fatal(err)
	}
	// price ₹500 + fee ₹25 (5%) = ₹525; the fee is GST-inclusive: 2500 = 2119 + 381 at 18%
	if r.PriceMinor != 50000 || r.FeeMinor != 2500 || r.FeeBaseMinor+r.FeeGSTMinor != r.FeeMinor || r.FeeGSTMinor != 381 || r.PaidMinor != 52500 {
		t.Fatalf("amounts: %+v", r)
	}
	if r.SellerGSTIN != "29ABCDE1234F1Z5" || r.Number == "" || r.BuyerEmail == "" {
		t.Fatalf("seller and number: %+v", r)
	}
	again, _ := e.svc.GetReceipt(ctx, b.ID, e.guest)
	if again.Number != r.Number || !again.IssuedAt.Equal(r.IssuedAt) {
		t.Fatalf("a receipt is issued once and stays the same: %s vs %s", again.Number, r.Number)
	}
	if _, err := e.svc.GetReceipt(ctx, b.ID, e.oth); err != ErrPaymentNotFound {
		t.Fatalf("other people can't read my receipt: %v", err)
	}
}

func TestReceiptHelpers(t *testing.T) {
	if financialYear(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)) != "2026-27" || financialYear(time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)) != "2026-27" || financialYear(time.Date(2027, 4, 1, 0, 0, 0, 0, time.UTC)) != "2027-28" {
		t.Fatal("Indian financial year runs April to March")
	}
	if b, g := splitGST(11800, 18); b != 10000 || g != 1800 {
		t.Fatalf("clean split: %d %d", b, g)
	}
	if b, g := splitGST(2500, 0); b != 2500 || g != 0 {
		t.Fatalf("no GST: %d %d", b, g)
	}
}

func TestRefundWebhook_AFailedRefundIsRetriedForTheDifference(t *testing.T) {
	e := newMoneyEnv(t, 5)
	ctx := context.Background()
	b := e.book(t, e.guest)
	order, _, _ := e.svc.CreateOrder(ctx, &Order{BookingID: b.ID}, e.guest, "9999999999", "", false)
	e.svc.MarkCaptured(ctx, order.ID, "pay_w_"+order.ID, order.AmountMinor)
	e.svc.RefundBookingIfCaptured(ctx, b.ID, "cancel", 100)
	if len(e.gw.refunds) != 1 {
		t.Fatalf("first attempt: %v", e.gw.refunds)
	}
	var refundID string
	e.pool.QueryRow(ctx, `SELECT r.id::text FROM refunds r JOIN payments p ON p.id=r.payment_id JOIN orders o ON o.id=p.order_id WHERE o.booking_id=$1`, b.ID).Scan(&refundID)

	if err := e.svc.RecordRefundResult(ctx, "not-ours", "FAILED"); err != nil {
		t.Fatalf("foreign ids are ignored: %v", err)
	}
	if err := e.svc.RecordRefundResult(ctx, refundID, "FAILED"); err != nil {
		t.Fatal(err)
	}
	// the gateway said it failed: it no longer counts, so a retry sends the money again
	if err := e.svc.RefundBookingIfCaptured(ctx, b.ID, "cancel", 100); err != nil || len(e.gw.refunds) != 2 || e.gw.refunds[1] != order.AmountMinor {
		t.Fatalf("retry after a failed refund: %v %v", err, e.gw.refunds)
	}
}

func TestReconcileOrders_SettlesAPaymentNobodyWasListeningFor(t *testing.T) {
	e := newMoneyEnv(t, 5)
	ctx := context.Background()
	b := e.book(t, e.guest)
	order, _, err := e.svc.CreateOrder(ctx, &Order{BookingID: b.ID}, e.guest, "9999999999", "", false)
	if err != nil {
		t.Fatal(err)
	}
	// the guest paid, then closed the app; the webhook never came
	e.gw.onlyOrder = order.ID
	e.gw.payments = []GatewayPayment{{ID: "pay_rec_" + order.ID, Status: "SUCCESS", AmountMinor: order.AmountMinor}}
	if _, err := e.pool.Exec(ctx, `UPDATE orders SET created_at = now() - interval '5 minutes' WHERE id = $1`, order.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ReconcileOrders(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if e.status(t, b.ID) != "confirmed" {
		t.Fatalf("a paid booking must be confirmed by the reconcile job, got %s", e.status(t, b.ID))
	}
	// running it again changes nothing
	if _, err := e.svc.ReconcileOrders(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if e.status(t, b.ID) != "confirmed" {
		t.Fatal("the second run must be a no-op")
	}
}
