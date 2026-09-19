package chat

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/pkg/media/mediatest"
)

type fakeScreener struct{}

func (fakeScreener) Screen(_ context.Context, body string) (string, string) {
	if strings.Contains(body, "BADWORD") {
		return "severe", "test"
	}
	return "", ""
}

type fakeReporter struct{}

func (fakeReporter) SubmitReportForSubject(context.Context, string, string, string, string) (string, error) {
	return "case-1", nil
}
func (fakeReporter) AutoFlagForSubject(context.Context, string, string, string, string, string) (string, error) {
	return "", nil
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hivemind:hivemind@localhost:5432/hivemind?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("skipping: %v", err)
	}
	return pool
}

type env struct {
	fm                             *mediatest.Fake
	ctx                            context.Context
	pool                           *pgxpool.Pool
	svc                            *Service
	host, member, outsider, planID string
	roomID                         string
}

func setup(t *testing.T) *env {
	t.Helper()
	pool := testPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	fm := mediatest.New(pool)
	e := &env{ctx: ctx, pool: pool, fm: fm,
		svc: NewService(NewRepository(pool), fakeReporter{}, fakeScreener{}, NewTemplateIcebreaker()).WithMedia(fm)}
	mk := func(label string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`,
			"ch-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com").Scan(&id); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		return id
	}
	e.host, e.member, e.outsider = mk("host"), mk("member"), mk("outsider")
	if err := pool.QueryRow(ctx, `INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, status)
		VALUES ('c', $1, now()+interval '1 day', now()+interval '2 days', 10, 'published') RETURNING id`, e.host).Scan(&e.planID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plan_participants (plan_id, user_id) VALUES ($1,$2)`, e.planID, e.member); err != nil {
		t.Fatalf("seed participant: %v", err)
	}
	room, err := e.svc.CreateRoom(ctx, e.planID, e.host, "user")
	if err != nil {
		t.Fatalf("host CreateRoom: %v", err)
	}
	e.roomID = room.ID
	if _, err := e.svc.CreateRoom(ctx, e.planID, e.member, "user"); err != nil {
		t.Fatalf("participant CreateRoom: %v", err)
	}
	return e
}

func TestCreateRoom_OnlyHostOrParticipant(t *testing.T) {
	e := setup(t)
	if _, err := e.svc.CreateRoom(e.ctx, e.planID, e.outsider, "user"); err != ErrNotAMember {
		t.Fatalf("a stranger must not create/join a plan's room, got %v", err)
	}
	if ok, _ := e.svc.repo.IsMember(e.ctx, e.roomID, e.outsider); ok {
		t.Fatal("the stranger must not have been added as a member")
	}
}

func TestChatIDORs_IcebreakerAndReportNeedMembership(t *testing.T) {
	e := setup(t)
	msg, err := e.svc.SendMessage(e.ctx, &Message{RoomID: e.roomID, SenderID: e.member, Body: "hello"}, "user")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := e.svc.GenerateIcebreaker(e.ctx, e.roomID, e.outsider); err != ErrNotAMember {
		t.Fatalf("icebreaker must be members-only, got %v", err)
	}
	if _, err := e.svc.ReportMessage(e.ctx, msg.ID, e.outsider, "spam"); err != ErrMessageNotFound {
		t.Fatalf("a non-member must not be able to report (or probe) a message, got %v", err)
	}
	if id, err := e.svc.ReportMessage(e.ctx, msg.ID, e.host, "spam"); err != nil || id == "" {
		t.Fatalf("a member can report: %q err=%v", id, err)
	}
}

func TestSendMessage_TypesValidationAndAnnouncements(t *testing.T) {
	e := setup(t)
	send := func(m *Message, sender string) error {
		m.RoomID, m.SenderID = e.roomID, sender
		_, err := e.svc.SendMessage(e.ctx, m, "user")
		return err
	}
	photo, clip := e.fm.Add(e.member, "image"), e.fm.Add(e.member, "video")
	if err := send(&Message{Type: "image", MediaIDs: []string{photo.ID, clip.ID}, Body: "cap"}, e.member); err != nil {
		t.Fatalf("image+video message: %v", err)
	}
	for name, m := range map[string]*Message{
		"image w/o media":              {Type: "image"},
		"a link is not media":          {Type: "image", MediaIDs: []string{"https://x/a.jpg"}},
		"someone else's media":         {Type: "image", MediaIDs: []string{e.fm.Add(e.host, "image").ID}},
		"11 attachments":               {Type: "image", MediaIDs: make([]string, 11)},
		"voice (no audio uploads yet)": {Type: "voice", MediaIDs: []string{e.fm.Add(e.member, "image").ID}, DurationSeconds: 12},
		"media on a text message":      {Type: "text", Body: "hi", MediaIDs: []string{e.fm.Add(e.member, "image").ID}},
		"location bad lat":             {Type: "location", Location: &Location{Latitude: 91}},
		"poll via SendMessage":         {Type: "poll", Body: "q"},
		"empty text":                   {Type: "text", Body: "  "},
	} {
		if err := send(m, e.member); err != ErrInvalidInput {
			t.Errorf("%s must be rejected, got %v", name, err)
		}
	}
	if err := send(&Message{Type: "location", Location: &Location{Latitude: 28.6, Longitude: 77.2, Label: "Belong"}}, e.member); err != nil {
		t.Fatalf("location: %v", err)
	}
	if err := send(&Message{Type: "text", Body: "BADWORD"}, e.member); err != ErrContentRejected {
		t.Fatalf("severe text is rejected, got %v", err)
	}
	if err := send(&Message{Type: "text", Body: "hi"}, e.outsider); err != ErrNotAMember {
		t.Fatalf("outsider can't post, got %v", err)
	}
	if err := send(&Message{Type: "announcement", Body: "Reservation under Belong"}, e.member); err != ErrNotHost {
		t.Fatalf("a participant must not announce, got %v", err)
	}
	if err := send(&Message{Type: "announcement", Body: "Reservation under Belong"}, e.host); err != nil {
		t.Fatalf("host announces: %v", err)
	}

	msgs, _, err := e.svc.ListMessages(e.ctx, e.roomID, e.member)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	seen := map[string]*Message{}
	for _, m := range msgs {
		seen[m.Type] = m
	}
	im := seen["image"]
	if im == nil || len(im.Media) != 2 || im.Media[0].Kind != "image" || im.Media[1].Kind != "video" || im.Media[1].DurationMS != 8000 || im.Media[0].ThumbURL == "" {
		t.Fatalf("photo+video message round-trips with kind/thumbnail/duration: %+v", im)
	}
	if seen["location"].Location == nil || seen["location"].Location.Label != "Belong" || seen["announcement"] == nil {
		t.Fatalf("messages must round-trip their type data: %+v", seen)
	}
}

func TestChatPoll_MembersOnlyAndVoteChange(t *testing.T) {
	e := setup(t)
	if _, err := e.svc.CreatePoll(e.ctx, e.roomID, e.outsider, "Where?", []string{"A", "B"}); err != ErrNotAMember {
		t.Fatalf("outsider can't create a poll, got %v", err)
	}
	for _, bad := range [][]string{{"only"}, {"a", "a"}, {"a", " "}, {"1", "2", "3", "4", "5", "6", "7"}} {
		if _, err := e.svc.CreatePoll(e.ctx, e.roomID, e.member, "Q?", bad); err != ErrInvalidInput {
			t.Errorf("options %v must be rejected, got %v", bad, err)
		}
	}
	msg, err := e.svc.CreatePoll(e.ctx, e.roomID, e.member, "Where to eat?", []string{"Belong", "Social"})
	if err != nil || msg.Poll == nil || len(msg.Poll.Options) != 2 {
		t.Fatalf("CreatePoll: %+v err=%v", msg, err)
	}
	p := msg.Poll
	if _, err := e.svc.VotePoll(e.ctx, p.ID, p.Options[0].ID, e.outsider); err != ErrNotAMember {
		t.Fatalf("outsider can't vote, got %v", err)
	}
	got, err := e.svc.VotePoll(e.ctx, p.ID, p.Options[0].ID, e.member)
	if err != nil || got.TotalVotes != 1 || got.MyVoteOptionID != p.Options[0].ID {
		t.Fatalf("vote: %+v err=%v", got, err)
	}
	got, err = e.svc.VotePoll(e.ctx, p.ID, p.Options[1].ID, e.member) // change of mind
	if err != nil || got.TotalVotes != 1 || got.Options[0].VoteCount != 0 || got.Options[1].VoteCount != 1 {
		t.Fatalf("changing a vote must move it, not add one: %+v err=%v", got, err)
	}
	if _, err := e.svc.VotePoll(e.ctx, p.ID, "00000000-0000-0000-0000-000000000000", e.member); err != ErrInvalidInput {
		t.Fatalf("an option from nowhere must be rejected, got %v", err)
	}
	// the poll hydrates in ListMessages with the viewer's own vote
	msgs, _, _ := e.svc.ListMessages(e.ctx, e.roomID, e.member)
	if len(msgs) == 0 || msgs[0].Poll == nil || msgs[0].Poll.MyVoteOptionID != p.Options[1].ID {
		t.Fatalf("list should carry the poll + my vote: %+v", msgs)
	}
}

func TestPinMessage_OnlyHostAndSameRoom(t *testing.T) {
	e := setup(t)
	msg, _ := e.svc.SendMessage(e.ctx, &Message{RoomID: e.roomID, SenderID: e.member, Body: "pin me"}, "user")

	if _, err := e.svc.PinMessage(e.ctx, e.roomID, msg.ID, false, e.member, "user"); err != ErrNotHost {
		t.Fatalf("a participant must not pin, got %v", err)
	}
	if _, err := e.svc.PinMessage(e.ctx, e.roomID, msg.ID, false, e.outsider, "user"); err != ErrNotAMember {
		t.Fatalf("an outsider must not pin, got %v", err)
	}
	if id, err := e.svc.PinMessage(e.ctx, e.roomID, msg.ID, false, e.host, "user"); err != nil || id != msg.ID {
		t.Fatalf("host pins: %q err=%v", id, err)
	}
	_, pinned, _ := e.svc.ListMessages(e.ctx, e.roomID, e.member)
	if pinned == nil || pinned.ID != msg.ID {
		t.Fatalf("ListMessages must return the pinned message, got %+v", pinned)
	}

	// a message from ANOTHER room can't be pinned here
	var other string
	e.pool.QueryRow(e.ctx, `INSERT INTO chat_rooms (plan_id) VALUES (NULL) RETURNING id`).Scan(&other)
	e.pool.Exec(e.ctx, `INSERT INTO chat_members (room_id, user_id) VALUES ($1,$2)`, other, e.member)
	foreign, _ := e.svc.SendMessage(e.ctx, &Message{RoomID: other, SenderID: e.member, Body: "elsewhere"}, "user")
	if _, err := e.svc.PinMessage(e.ctx, e.roomID, foreign.ID, false, e.host, "user"); err != ErrMessageNotFound {
		t.Fatalf("cross-room pin must be refused, got %v", err)
	}
	if id, err := e.svc.PinMessage(e.ctx, e.roomID, "", true, e.host, "user"); err != nil || id != "" {
		t.Fatalf("unpin: %q err=%v", id, err)
	}
	if _, pinned, _ = e.svc.ListMessages(e.ctx, e.roomID, e.member); pinned != nil {
		t.Fatal("nothing should be pinned after unpin")
	}
}

func TestListMessages_HidesBlockedSenders(t *testing.T) {
	e := setup(t)
	e.svc.SendMessage(e.ctx, &Message{RoomID: e.roomID, SenderID: e.member, Body: "from member"}, "user")
	e.svc.SendMessage(e.ctx, &Message{RoomID: e.roomID, SenderID: e.host, Body: "from host"}, "user")
	e.pool.Exec(e.ctx, `INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, e.host, e.member)

	msgs, _, err := e.svc.ListMessages(e.ctx, e.roomID, e.host)
	if err != nil || len(msgs) != 1 || msgs[0].Body != "from host" {
		t.Fatalf("the host blocked the member, so the member's messages must be hidden from the host: %+v err=%v", msgs, err)
	}
	if msgs, _, _ = e.svc.ListMessages(e.ctx, e.roomID, e.member); len(msgs) != 1 || msgs[0].Body != "from member" {
		t.Fatalf("and hidden the other way round too: %+v", msgs)
	}
}
