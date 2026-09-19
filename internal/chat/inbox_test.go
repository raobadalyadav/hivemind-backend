package chat

import (
	"testing"
	"time"
)

func (e *env) user(label string) string {
	var id string
	if err := e.pool.QueryRow(e.ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"in-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com").Scan(&id); err != nil {
		panic(err)
	}
	e.pool.Exec(e.ctx, `INSERT INTO user_profiles (user_id, display_name) VALUES ($1,$2)`, id, label)
	return id
}

func (e *env) connect(a, b string) {
	e.pool.Exec(e.ctx, `INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,'accepted')`, a, b)
}

func find(cs []Summary, id string) *Summary {
	for i := range cs {
		if cs[i].RoomID == id {
			return &cs[i]
		}
	}
	return nil
}

func TestInbox_PrimaryGeneralUnreadAndBlocks(t *testing.T) {
	e := setup(t)
	me, friend, stranger := e.user("me"), e.user("friend"), e.user("stranger")
	e.connect(me, friend)

	// DM with a friend, an upcoming plan chat I'm in, an ad-hoc group, and an ended plan's chat.
	dm, err := e.svc.OpenDirectChat(e.ctx, me, friend)
	if err != nil {
		t.Fatalf("OpenDirectChat: %v", err)
	}
	e.pool.Exec(e.ctx, `INSERT INTO plan_participants (plan_id, user_id) VALUES ($1,$2)`, e.planID, me)
	if err := e.svc.EnsureMembership(e.ctx, e.planID, me); err != nil {
		t.Fatalf("EnsureMembership: %v", err)
	}
	group, _ := e.svc.CreateAdHocRoomWithMembers(e.ctx, []string{me, friend, stranger})
	var pastPlan string
	e.pool.QueryRow(e.ctx, `INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, status) VALUES ('Old dinner', $1, now()-interval '3 days', now()-interval '2 days', 5, 'completed') RETURNING id`, e.host).Scan(&pastPlan)
	pastRoom, _ := e.svc.CreateRoom(e.ctx, pastPlan, e.host, "user")
	e.svc.EnsureMembership(e.ctx, pastPlan, me)

	primary, pu, gu, err := e.svc.Inbox(e.ctx, me, true)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	general, _, _, _ := e.svc.Inbox(e.ctx, me, false)
	if find(primary, dm) == nil || find(primary, e.roomID) == nil {
		t.Fatalf("Primary = friend DM + upcoming plan chat, got %+v", primary)
	}
	if find(general, group) == nil || find(general, pastRoom.ID) == nil {
		t.Fatalf("General = groups + ended plans, got %+v", general)
	}
	if find(primary, group) != nil || find(primary, pastRoom.ID) != nil || find(general, dm) != nil {
		t.Fatal("a chat is in exactly one tab")
	}
	d := find(primary, dm)
	if d.Kind != "dm" || d.Title != "friend" || d.OtherUserID != friend {
		t.Fatalf("a DM is titled by the other person: %+v", d)
	}
	if p := find(primary, e.roomID); p.Kind != "plan" || p.Title != "c" || p.PlanID != e.planID {
		t.Fatalf("a plan chat is titled by the plan: %+v", p)
	}
	if g := find(general, group); g.Title == "" {
		t.Fatalf("a group is titled by its members: %+v", g)
	}

	// unread: others' messages after my read marker; my own and blocked senders' don't count
	e.svc.SendMessage(e.ctx, &Message{RoomID: dm, SenderID: friend, Body: "hey!"}, "user")
	e.svc.SendMessage(e.ctx, &Message{RoomID: dm, SenderID: friend, Body: "you there?"}, "user")
	e.svc.SendMessage(e.ctx, &Message{RoomID: dm, SenderID: me, Body: "yes"}, "user")
	e.svc.SendMessage(e.ctx, &Message{RoomID: group, SenderID: stranger, Body: "hello group"}, "user")
	primary, pu, gu, _ = e.svc.Inbox(e.ctx, me, true)
	d = find(primary, dm)
	if d.Unread != 2 || d.LastMessage != "yes" || pu != 2 || gu != 1 {
		t.Fatalf("2 unread in the DM, last preview is my own message; totals 2/1: %+v pu=%d gu=%d", d, pu, gu)
	}
	if primary[0].RoomID != dm {
		t.Fatalf("most recent activity first, got %s", primary[0].RoomID)
	}
	photo := e.fm.Add(friend, "image")
	e.svc.SendMessage(e.ctx, &Message{RoomID: dm, SenderID: friend, Type: "image", MediaIDs: []string{photo.ID}}, "user")
	primary, _, _, _ = e.svc.Inbox(e.ctx, me, true)
	if p := find(primary, dm); p.LastMessage != "📷 Photo" {
		t.Fatalf("media previews as Photo, got %q", p.LastMessage)
	}

	if err := e.svc.MarkRead(e.ctx, dm, me); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	primary, pu, _, _ = e.svc.Inbox(e.ctx, me, true)
	if find(primary, dm).Unread != 0 || pu != 0 {
		t.Fatal("marking read clears the unread count")
	}
	if err := e.svc.MarkRead(e.ctx, dm, stranger); err != ErrNotAMember {
		t.Fatalf("a non-member can't mark someone else's room read, got %v", err)
	}

	// blocks: messages from a blocked user don't count; a blocked DM disappears and can't be written to
	e.pool.Exec(e.ctx, `INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, me, stranger)
	e.svc.SendMessage(e.ctx, &Message{RoomID: group, SenderID: stranger, Body: "again"}, "user")
	_, _, gu, _ = e.svc.Inbox(e.ctx, me, true)
	if gu != 0 {
		t.Fatalf("once blocked, none of that person's messages count as unread: %d", gu)
	}
	e.pool.Exec(e.ctx, `INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, friend, me)
	primary, _, _, _ = e.svc.Inbox(e.ctx, me, true)
	if find(primary, dm) != nil {
		t.Fatal("a DM with someone who blocked me is hidden")
	}
	if _, err := e.svc.SendMessage(e.ctx, &Message{RoomID: dm, SenderID: me, Body: "hello?"}, "user"); err != ErrNotAMember {
		t.Fatalf("can't message someone who blocked you, got %v", err)
	}
}

func TestOpenDirectChat_ConnectedOnlyAndIdempotent(t *testing.T) {
	e := setup(t)
	a, b, c := e.user("a"), e.user("b"), e.user("c")
	e.connect(a, b)
	e.pool.Exec(e.ctx, `INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,'pending')`, a, c)

	if _, err := e.svc.OpenDirectChat(e.ctx, a, c); err != ErrNotConnected {
		t.Fatalf("a pending request isn't consent to message, got %v", err)
	}
	if _, err := e.svc.OpenDirectChat(e.ctx, c, a); err != ErrNotConnected {
		t.Fatalf("nor from the other side, got %v", err)
	}
	if _, err := e.svc.OpenDirectChat(e.ctx, a, e.outsider); err != ErrNotConnected {
		t.Fatalf("strangers can't be messaged, got %v", err)
	}
	if _, err := e.svc.OpenDirectChat(e.ctx, a, a); err != ErrInvalidInput {
		t.Fatalf("not yourself, got %v", err)
	}
	r1, err := e.svc.OpenDirectChat(e.ctx, a, b)
	r2, _ := e.svc.OpenDirectChat(e.ctx, b, a) // either direction, same room
	if err != nil || r1 != r2 {
		t.Fatalf("one DM room per pair: %q vs %q err=%v", r1, r2, err)
	}
	var members int
	e.pool.QueryRow(e.ctx, `SELECT count(*) FROM chat_members WHERE room_id = $1`, r1).Scan(&members)
	if members != 2 {
		t.Fatalf("exactly the two people are members, got %d", members)
	}
	e.pool.Exec(e.ctx, `INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, b, a)
	if _, err := e.svc.OpenDirectChat(e.ctx, a, b); err != ErrNotConnected {
		t.Fatalf("a block ends the ability to (re)open a chat, got %v", err)
	}
}
