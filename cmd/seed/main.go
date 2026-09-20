// Command seed fills a LOCAL development database with realistic demo data:
// people with photos, plans over the next four weeks, posts, stories,
// communities, a small social graph (friends, requests, incoming waves, DMs)
// and events — all in one city, so the mobile app looks alive.
//
//	go run ./cmd/seed -keeper <user-uuid>
//
// The keeper is YOUR account: its own rows are never modified, only linked to
// (a few friends, pending requests, waves and two plan bookings are added so
// Home, Meet, Chat and Plans have something to show). Everything the tool
// creates is tagged with the e-mail domain @seed.hivemind.local, so running it
// again deletes and recreates only its own rows.
//
// It also tidies reference data: renames/activates the keeper's city (test
// cities have junk names), adds a few real Indian cities, and drops unused
// test cities and categories. Development only: refuses unless APP_ENV is
// empty or "dev". Photos are hot-linked from randomuser.me and picsum.photos,
// so the phone needs internet to show them.
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/config"
)

const seedDomain = "@seed.hivemind.local"

type person struct {
	first, last, gender, job, edu string
	born                          int
	bio                           string
}

var people = []person{
	{"Aarav", "Mehta", "male", "Product designer at a fintech", "NID Ahmedabad", 1996, "Filter coffee first, Figma second. New to the city and keen on weekend treks and board-game nights."},
	{"Diya", "Nair", "female", "Data scientist", "IISc Bengaluru", 1997, "I collect playlists and half-finished sketchbooks. Always up for a long brunch."},
	{"Kabir", "Sharma", "male", "Startup founder (climate tech)", "IIT Bombay", 1994, "Building something for the planet, running Cubbon Park loops before sunrise."},
	{"Ananya", "Iyer", "female", "UX researcher", "Christ University", 1998, "Museum dates, street food, terrible puns. Ask me about my plant collection."},
	{"Rohan", "Kulkarni", "male", "Backend engineer", "PES University", 1995, "Cricket on weekends, cooking experiments on weeknights. Will trade recipes for recommendations."},
	{"Meera", "Reddy", "female", "Doctor (resident)", "St. John's Medical College", 1996, "Sleep-deprived but social. Weekend hikes are how I reset."},
	{"Arjun", "Bhat", "male", "Photographer", "Self-taught", 1993, "Film cameras, chai stalls and golden hour. Happy to shoot your next meetup."},
	{"Ishita", "Banerjee", "female", "Brand strategist", "Symbiosis Pune", 1997, "Bookshop crawler. Currently reading too many things at once."},
	{"Vikram", "Singh", "male", "Musician", "Berklee Online", 1992, "Guitar, open mics and late-night jam sessions. Bring an instrument or just good vibes."},
	{"Sana", "Khan", "female", "Chef de partie", "Culinary Academy of India", 1995, "Feeding people is my love language. Dinner clubs are my happy place."},
	{"Neel", "Desai", "male", "Investment analyst", "IIM Bangalore", 1994, "Numbers by day, football by night. Looking for a Sunday five-a-side crew."},
	{"Tara", "Menon", "female", "Yoga teacher", "Kaivalyadhama", 1993, "Sunrise flows and slow mornings. Let's walk and talk."},
	{"Aditya", "Rao", "male", "Game developer", "IIIT Hyderabad", 1999, "Indie games, board games, any games. I will teach you Catan gently."},
	{"Kavya", "Pillai", "female", "Journalist", "ACJ Chennai", 1996, "Curious about everyone's story. Best conversations happen over dosa."},
	{"Rahul", "Verma", "male", "Architect", "CEPT Ahmedabad", 1991, "Sketching buildings, chasing good light. Weekend cycling routes welcome."},
	{"Priya", "Shetty", "female", "Marketing manager", "Manipal University", 1998, "Coastal soul in a landlocked city. Beach trip planner in residence."},
	{"Siddharth", "Joshi", "male", "Filmmaker", "FTII Pune", 1992, "Short films, long walks. Always casting for friends who like the outdoors."},
	{"Nisha", "Kapoor", "female", "Fitness coach", "Cult.fit", 1997, "Run club regular. Coffee after every long run, non-negotiable."},
	{"Manav", "Agarwal", "male", "Consultant", "SRCC Delhi", 1995, "Travel-obsessed, packing light. Ask me about my 12-hour train stories."},
	{"Zoya", "Ali", "female", "Illustrator", "Srishti Manipal", 1999, "Doodles on napkins, gouache on weekends. Cafés with good light only."},
	{"Harsh", "Patel", "male", "Cloud engineer", "NIT Surat", 1996, "Trek, tech, tea. Currently planning Kudremukh."},
	{"Lakshmi", "Krishnan", "female", "Classical dancer", "Kalakshetra", 1994, "Bharatanatyam and Bollywood in equal parts. Dance-floor friendly."},
	{"Dev", "Malhotra", "male", "Comedy writer", "Delhi University", 1993, "Open-mic regular. I promise to be funnier in person than in this bio."},
	{"Riya", "Sen", "female", "Biotech researcher", "NCBS Bengaluru", 1998, "Lab coat off, hiking boots on. Wildlife photography is my side quest."},
}

var interestPool = []string{"Food", "Coffee", "Sports", "Fitness", "Travel", "Music", "Movies", "Gaming", "Books", "Technology", "Startups", "Art", "Photography", "Adventure", "Networking"}
var hobbyPool = []string{"Cooking", "Trekking", "Board games", "Painting", "Running", "Cycling", "Guitar", "Journaling", "Baking", "Yoga", "Stand-up comedy", "Gardening", "Pottery", "Chess", "Badminton", "Dancing"}

type venue struct {
	name, addr string
	lat, lng   float64
}

var venues = []venue{
	{"Third Wave Coffee, Indiranagar", "100 Feet Rd, Indiranagar", 12.9784, 77.6408},
	{"Toit Brewpub", "298 100 Feet Rd, Indiranagar", 12.9791, 77.6407},
	{"Cubbon Park (Bandstand)", "Kasturba Rd, Ambedkar Veedhi", 12.9763, 77.5929},
	{"Lalbagh Botanical Garden", "Mavalli, Lalbagh West Gate", 12.9507, 77.5848},
	{"Church Street Social", "46/1 Church St", 12.9752, 77.6068},
	{"Windmills Craftworks", "Whitefield Main Rd", 12.9866, 77.7300},
	{"Cult.fit Koramangala", "80 Feet Rd, Koramangala", 12.9352, 77.6245},
	{"Ranga Shankara", "JP Nagar 2nd Phase", 12.9107, 77.5850},
	{"The Hive, Koramangala", "5th Block, Koramangala", 12.9345, 77.6196},
	{"Vidyarthi Bhavan", "Gandhi Bazaar, Basavanagudi", 12.9430, 77.5713},
	{"Bangalore Turf Club Grounds", "Racecourse Rd", 12.9784, 77.5825},
	{"Ulsoor Lake", "Halasuru", 12.9826, 77.6209},
}

type plan struct {
	title, desc, cat string
	hour, dur, cap   int
	price            int64 // rupees
	approval         bool
}

var plans = []plan{
	{"Sundowner & board games on a rooftop", "Craft beer, Catan and Codenames as the sun goes down. Beginners very welcome — we'll teach you.", "Drinks", 18, 3, 12, 499, false},
	{"Long-table dinner: 8 strangers, one tasting menu", "A chef-led five-course dinner at one big table. Come hungry, leave with new friends.", "Dinner", 20, 3, 8, 1499, true},
	{"Filter coffee & first-time-in-Bangalore meetup", "New to the city? Meet other newcomers over South Indian filter coffee and tell your Bengaluru stories.", "Coffee", 10, 2, 10, 0, false},
	{"Sunday brunch club", "Unlimited dosas, good playlists and even better conversation. Bring an appetite and a friend (or come solo).", "Brunch", 11, 2, 14, 799, false},
	{"Cubbon Park sunrise walk", "A relaxed 5 km walk through the park followed by chai. No pace pressure, just company.", "Walk", 6, 2, 20, 0, false},
	{"Weekend 5-a-side football", "Friendly, mixed-level five-a-side on turf. Bibs provided, water on us.", "Sports", 7, 2, 14, 250, false},
	{"Beginner film photography walk", "Bring any camera (phone is fine). We'll walk Lalbagh, talk light and composition, and review shots over coffee.", "Photography", 16, 3, 12, 599, false},
	{"Indie night out: live acoustic sets", "Three local artists, one cosy room. Come for the music, stay for the crowd.", "Party", 19, 4, 30, 399, false},
	{"Pottery workshop for beginners", "Hands-on wheel throwing with a studio instructor. You take home what you make.", "Workshop", 15, 3, 10, 1199, false},
	{"Open-mic comedy: watch or perform", "Five-minute sets or just laughs from the audience. Supportive room, zero pressure.", "Drinks", 19, 3, 25, 199, false},
	{"Trek to Nandi Hills for sunrise", "Early start, easy trail, big views. Carpool from Hebbal; breakfast at the top.", "Adventure", 4, 6, 16, 349, true},
	{"Startup founders' coffee circle", "Small-group, no-slides conversations about building things. Bring one problem you're stuck on.", "Networking", 9, 2, 12, 0, false},
	{"Cook-along: Kerala fish curry night", "Cook dinner together, then eat it. Vegetarian option available.", "Dinner", 19, 3, 10, 899, false},
	{"Run club: 8 km easy pace + chai", "All paces welcome — nobody gets left behind. Chai stop at the end.", "Fitness", 6, 2, 25, 0, false},
	{"Trivia night: teams of four", "Movies, music, science and a very unfair round about Bangalore. Teams formed on the spot.", "Party", 19, 3, 24, 299, false},
	{"Sketch & sip at a café", "Draw whatever you see. Materials provided; skill not required.", "Art", 16, 2, 12, 399, false},
}

var postTexts = []string{
	"Found the best benne dose in Basavanagudi this morning. Worth the queue. Who's joining next Sunday?",
	"Sunrise at Cubbon Park never gets old. Tomorrow's walk still has a few spots open 🌅",
	"Trying to learn the guitar for the third time. Any open-mic tips for a total beginner?",
	"New in Bengaluru and already addicted to filter coffee. Send recommendations!",
	"Our board-game night last week ran until midnight. Someone please stop me from buying another Catan expansion.",
	"Weekend trek planning: Kudremukh or Skandagiri? Vote in the comments.",
	"Golden hour at Lalbagh — shot on a 30-year-old film camera. Grain and all.",
	"Cooked dinner for six strangers tonight. Six became friends by dessert. 🍛",
	"Five-a-side this Sunday, two spots left. Bring water and bad jokes.",
	"Reading recommendations for a long train ride? Fiction preferred.",
	"That moment when the whole table goes quiet because the biryani arrived.",
	"Started a run club with three people last month. Eleven turned up today!",
	"Anyone up for a pottery workshop this weekend? First-timers welcome.",
	"Rooftop views + cold beer + friendly strangers. Perfect Saturday.",
	"Open-mic night was unreal. Proud of everyone who took the stage for the first time.",
	"Looking for a photography buddy for a Sunday street walk in Chickpet.",
	"Made a playlist for long walks. Sharing it at tomorrow's meetup.",
	"Tried indoor climbing for the first time and now my forearms hate me. Worth it.",
	"Who else is up early on weekends? Let's do a sunrise walk.",
	"The best conversations happen on the third cup of coffee.",
}

var commentTexts = []string{"Count me in!", "This looks amazing 😍", "Which place is this?", "Saving this for the weekend", "So true", "I'm in for next time", "Love this", "Send me the details!", "Great shot", "Same here, ha!"}

var externalEvents = []struct{ title, desc, venue, cat, url string }{
	{"Bangalore Comedy Festival", "Two nights of stand-up from India's funniest, including new-material sets.", "Phoenix Marketcity, Whitefield", "Party", "https://in.bookmyshow.com/"},
	{"Sunday Soul Sante Market", "Handmade goods, food stalls and live music across 200 stalls.", "Jayamahal Palace Hotel", "Food", "https://www.sundaysoulsante.com/"},
	{"Indie Music Weekender", "Six indie bands across two stages, food trucks and art installations.", "Jawaharlal Nehru Planetarium Grounds", "Music", "https://insider.in/"},
	{"Bengaluru Book Fair", "Independent publishers, author talks and a big second-hand bookshop.", "Palace Grounds", "Books", "https://insider.in/"},
	{"Startup Grind Bengaluru", "Fireside chat with founders followed by open networking.", "91springboard, Koramangala", "Networking", "https://www.startupgrind.com/"},
	{"Cubbon Park Heritage Walk", "A guided historical walk through the park's colonial buildings.", "Cubbon Park", "Adventure", "https://insider.in/"},
}

var communities = []struct{ name, desc string }{
	{"Bengaluru Coffee Snobs", "Third-wave cafés, pour-overs and the eternal filter-coffee debate."},
	{"Weekend Trekkers", "Sunrise treks and easy hikes around Karnataka. All levels."},
	{"Board Game Nights BLR", "Weekly game nights, from Catan to Codenames."},
	{"Indie Music Circle", "Open mics, jam sessions and gig buddies."},
	{"Founders & Friends", "Early-stage builders sharing wins, losses and introductions."},
}

func main() {
	keeper := flag.String("keeper", "", "your user id (its own data is never modified)")
	cityName := flag.String("city", "Bengaluru", "name for the keeper's city")
	flag.Parse()
	if env := os.Getenv("APP_ENV"); env != "" && env != "dev" {
		fatal(fmt.Errorf("refusing to run when APP_ENV=%s", env))
	}
	if *keeper == "" {
		fatal(fmt.Errorf("-keeper <user-uuid> is required"))
	}
	ctx := context.Background()
	cfg := config.Load()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		fatal(err)
	}
	defer tx.Rollback(ctx)

	s := &seeder{ctx: ctx, tx: tx, rng: rand.New(rand.NewSource(42)), keeper: *keeper, now: time.Now()}
	steps := []struct {
		name string
		fn   func() error
	}{
		{"clean previous seed", s.clean},
		{"cities & categories", func() error { return s.reference(*cityName) }},
		{"people", s.seedPeople},
		{"venues & plans", s.seedPlans},
		{"social graph", s.seedGraph},
		{"posts & stories", s.seedFeed},
		{"chats", s.seedChats},
		{"communities & events", s.seedCommunitiesEvents},
	}
	for _, st := range steps {
		if err := st.fn(); err != nil {
			fatal(fmt.Errorf("%s: %w", st.name, err))
		}
		fmt.Println("✓", st.name)
	}
	if err := tx.Commit(ctx); err != nil {
		fatal(err)
	}
	fmt.Printf("done: %d people, %d plans, %d posts in %s (keeper %s)\n", len(s.users), len(s.plans), s.posts, *cityName, *keeper)
}

type seeder struct {
	ctx    context.Context
	tx     pgx.Tx
	rng    *rand.Rand
	keeper string
	now    time.Time
	city   string
	users  []string // seed user ids, index-aligned with `people`
	plans  []string
	venues []string
	cats   map[string]string
	posts  int
}

func (s *seeder) exec(sql string, args ...any) error {
	_, err := s.tx.Exec(s.ctx, sql, args...)
	return err
}

func (s *seeder) one(sql string, args ...any) (string, error) {
	var id string
	err := s.tx.QueryRow(s.ctx, sql, args...).Scan(&id)
	return id, err
}

// tryExec runs sql in a savepoint and ignores failure (used to drop reference
// rows that may still be referenced).
func (s *seeder) tryExec(sql string, args ...any) {
	sp, err := s.tx.Begin(s.ctx)
	if err != nil {
		return
	}
	if _, err := sp.Exec(s.ctx, sql, args...); err != nil {
		sp.Rollback(s.ctx)
		return
	}
	sp.Commit(s.ctx)
}

const seedUsers = `(SELECT id FROM users WHERE email LIKE '%@seed.hivemind.local')`

func (s *seeder) clean() error {
	stmts := []string{
		`DELETE FROM external_events WHERE source = 'seed'`,
		`DELETE FROM messages WHERE sender_id IN ` + seedUsers,
		`DELETE FROM chat_rooms WHERE id IN (SELECT room_id FROM chat_members WHERE user_id IN ` + seedUsers + `) OR plan_id IN (SELECT id FROM plans WHERE host_id IN ` + seedUsers + `)`,
		`DELETE FROM likes WHERE user_id IN ` + seedUsers + ` OR post_id IN (SELECT id FROM posts WHERE author_id IN ` + seedUsers + `)`,
		`DELETE FROM comments WHERE author_id IN ` + seedUsers + ` OR post_id IN (SELECT id FROM posts WHERE author_id IN ` + seedUsers + `)`,
		`DELETE FROM post_saves WHERE user_id IN ` + seedUsers + ` OR post_id IN (SELECT id FROM posts WHERE author_id IN ` + seedUsers + `)`,
		`DELETE FROM post_media WHERE post_id IN (SELECT id FROM posts WHERE author_id IN ` + seedUsers + `)`,
		`DELETE FROM posts WHERE author_id IN ` + seedUsers,
		`DELETE FROM stories WHERE author_id IN ` + seedUsers,
		`DELETE FROM community_members WHERE user_id IN ` + seedUsers + ` OR community_id IN (SELECT id FROM communities WHERE owner_id IN ` + seedUsers + `)`,
		`DELETE FROM communities WHERE owner_id IN ` + seedUsers,
		`DELETE FROM bookings WHERE user_id IN ` + seedUsers + ` OR plan_id IN (SELECT id FROM plans WHERE host_id IN ` + seedUsers + `)`,
		`DELETE FROM plans WHERE host_id IN ` + seedUsers,
		`DELETE FROM venues WHERE owner_host_id IN ` + seedUsers,
		`DELETE FROM connections WHERE requester_id IN ` + seedUsers + ` OR recipient_id IN ` + seedUsers,
		`DELETE FROM swipes WHERE actor_id IN ` + seedUsers + ` OR target_id IN ` + seedUsers,
		`DELETE FROM profile_photos WHERE user_id IN ` + seedUsers,
		`DELETE FROM user_preferences WHERE user_id IN ` + seedUsers,
		`DELETE FROM user_profiles WHERE user_id IN ` + seedUsers,
		`DELETE FROM users WHERE email LIKE '%@seed.hivemind.local'`,
	}
	for _, q := range stmts {
		if err := s.exec(q); err != nil {
			return fmt.Errorf("%s: %w", q[:40], err)
		}
	}
	return nil
}

func (s *seeder) reference(cityName string) error {
	city, err := s.one(`SELECT city_id::text FROM users WHERE id = $1`, s.keeper)
	if err != nil || city == "" {
		return fmt.Errorf("keeper %s has no city (or doesn't exist): %v", s.keeper, err)
	}
	s.city = city
	// the keeper's city is usually a test-fixture name; make it a real, active city
	if err := s.exec(`UPDATE cities SET name=$2, state='Karnataka', country='IN', status='active', timezone='Asia/Kolkata',
		centroid = ST_SetSRID(ST_MakePoint(77.5946, 12.9716), 4326)::geography WHERE id = $1`, city, cityName); err != nil {
		return err
	}
	for _, c := range []struct {
		name, state string
		lng, lat    float64
	}{{"Mumbai", "Maharashtra", 72.8777, 19.0760}, {"Hyderabad", "Telangana", 78.4867, 17.3850}, {"Pune", "Maharashtra", 73.8567, 18.5204}, {"Chennai", "Tamil Nadu", 80.2707, 13.0827}} {
		if err := s.exec(`INSERT INTO cities (name, state, country, status, timezone, centroid)
			SELECT $1, $2, 'IN', 'active', 'Asia/Kolkata', ST_SetSRID(ST_MakePoint($3,$4),4326)::geography
			WHERE NOT EXISTS (SELECT 1 FROM cities WHERE name = $1)`, c.name, c.state, c.lng, c.lat); err != nil {
			return err
		}
	}
	// drop unused test cities/categories (rows still referenced are simply kept)
	rows, err := s.tx.Query(s.ctx, `SELECT id::text FROM cities WHERE id <> $1 AND name ~ '(Test|Meet City|Other [0-9]|Live Ev|Intent City|Home City|Travel City|Admin City|Event City|Up City|NewLaunch|Grpcurl|P5Home)'`, city)
	if err != nil {
		return err
	}
	var junk []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		junk = append(junk, id)
	}
	rows.Close()
	for _, id := range junk {
		s.tryExec(`DELETE FROM cities WHERE id = $1`, id)
	}
	rows, err = s.tx.Query(s.ctx, `SELECT id::text FROM categories WHERE name LIKE 'UpCat%'`)
	if err != nil {
		return err
	}
	var junkCats []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		junkCats = append(junkCats, id)
	}
	rows.Close()
	for _, id := range junkCats {
		s.tryExec(`DELETE FROM categories WHERE id = $1`, id)
	}
	s.cats = map[string]string{}
	rows, err = s.tx.Query(s.ctx, `SELECT name, id::text FROM categories`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n, id string
		rows.Scan(&n, &id)
		s.cats[n] = id
	}
	return nil
}

func (s *seeder) seedPeople() error {
	for i, p := range people {
		email := strings.ToLower(p.first+"."+p.last) + seedDomain
		dob := fmt.Sprintf("%d-%02d-%02d", p.born, 1+s.rng.Intn(12), 1+s.rng.Intn(28))
		id, err := s.one(`INSERT INTO users (email, city_id, date_of_birth, age_verified, status) VALUES ($1,$2,$3::date,true,'active') RETURNING id::text`, email, s.city, dob)
		if err != nil {
			return err
		}
		s.users = append(s.users, id)
		interests := pick(s.rng, interestPool, 5+s.rng.Intn(4))
		hobbies := pick(s.rng, hobbyPool, 2+s.rng.Intn(3))
		var verified any
		if i%5 < 2 { // ~40% carry the blue tick
			verified = s.now.Add(-time.Duration(24*(1+s.rng.Intn(30))) * time.Hour)
		}
		if err := s.exec(`INSERT INTO user_profiles (user_id, display_name, bio, occupation, education, gender, interests, hobbies, languages, selfie_verified_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, p.first+" "+p.last, p.bio, p.job, p.edu, p.gender, interests, hobbies, []string{"English", "Hindi"}, verified); err != nil {
			return err
		}
		intents := pick(s.rng, []string{"friends", "activity_partners", "networking", "dating"}, 1+s.rng.Intn(2))
		if err := s.exec(`INSERT INTO user_preferences (user_id, intents) VALUES ($1,$2)`, id, intents); err != nil {
			return err
		}
		g := "women"
		if p.gender == "male" {
			g = "men"
		}
		n := (i*7)%90 + 1
		photos := [][2]string{
			{fmt.Sprintf("https://randomuser.me/api/portraits/%s/%d.jpg", g, n), fmt.Sprintf("https://randomuser.me/api/portraits/%s/%d.jpg", g, n)},
			{fmt.Sprintf("https://picsum.photos/seed/%s-a/800/1000", strings.ToLower(p.first)), fmt.Sprintf("https://picsum.photos/seed/%s-a/300/375", strings.ToLower(p.first))},
			{fmt.Sprintf("https://picsum.photos/seed/%s-b/800/1000", strings.ToLower(p.first)), fmt.Sprintf("https://picsum.photos/seed/%s-b/300/375", strings.ToLower(p.first))},
		}
		for pos, ph := range photos {
			if err := s.exec(`INSERT INTO profile_photos (user_id, url, thumb_url, position, width, height) VALUES ($1,$2,$3,$4,800,1000)`, id, ph[0], ph[1], pos); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *seeder) seedPlans() error {
	for _, v := range venues {
		id, err := s.one(`INSERT INTO venues (city_id, name, address, location, capacity, owner_host_id) VALUES ($1,$2,$3,ST_SetSRID(ST_MakePoint($4,$5),4326)::geography,$6,$7) RETURNING id::text`,
			s.city, v.name, v.addr, v.lng, v.lat, 40+s.rng.Intn(60), s.users[s.rng.Intn(len(s.users))])
		if err != nil {
			return err
		}
		s.venues = append(s.venues, id)
	}
	// 32 plans spread over 28 days, with a dense this-week and next-week
	offsets := []int{0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 7, 7, 8, 8, 9, 10, 10, 11, 12, 13, 14, 15, 17, 18, 20, 21, 22, 24, 25, 27}
	for i, off := range offsets {
		t := plans[i%len(plans)]
		hostIdx := (i * 5) % len(s.users)
		vi := s.rng.Intn(len(venues))
		start := time.Date(s.now.Year(), s.now.Month(), s.now.Day(), t.hour, 0, 0, 0, time.Local).AddDate(0, 0, off)
		if start.Before(s.now.Add(2 * time.Hour)) {
			start = start.AddDate(0, 0, 1)
		}
		join := "open"
		if t.approval {
			join = "approval"
		}
		slug := strings.ReplaceAll(strings.ToLower(t.cat), " ", "") + fmt.Sprint(i)
		id, err := s.one(`INSERT INTO plans (city_id, host_id, venue_id, title, description, category_id, starts_at, ends_at, capacity, price_minor, currency, status, location, join_mode, visibility, cover_url, cover_thumb_url)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'INR','published',ST_SetSRID(ST_MakePoint($11,$12),4326)::geography,$13::plan_join_mode,'public',$14,$15) RETURNING id::text`,
			s.city, s.users[hostIdx], s.venues[vi], t.title, t.desc, s.cats[t.cat], start, start.Add(time.Duration(t.dur)*time.Hour), t.cap, t.price*100,
			venues[vi].lng, venues[vi].lat, join,
			"https://picsum.photos/seed/"+slug+"/800/450", "https://picsum.photos/seed/"+slug+"/300/170")
		if err != nil {
			return err
		}
		s.plans = append(s.plans, id)
		// some seats already taken by other people; the two soonest also have the keeper
		taken := 1 + s.rng.Intn(t.cap*2/3)
		joiners := s.rng.Perm(len(s.users))
		n := 0
		for _, j := range joiners {
			if n >= taken || j == hostIdx {
				continue
			}
			if err := s.exec(`INSERT INTO bookings (plan_id, user_id, status, price_minor, currency, idempotency_key) VALUES ($1,$2,'confirmed',$3,'INR',$4)`,
				id, s.users[j], t.price*100, "seed-"+id+"-"+s.users[j]); err != nil {
				return err
			}
			n++
		}
		if i == 1 || i == 3 { // keeper is going to two plans (approval-only ones excluded)
			if !t.approval {
				if err := s.exec(`INSERT INTO bookings (plan_id, user_id, status, price_minor, currency, idempotency_key) VALUES ($1,$2,'confirmed',$3,'INR',$4) ON CONFLICT DO NOTHING`,
					id, s.keeper, t.price*100, "seed-"+id+"-keeper"); err != nil {
					return err
				}
			}
		}
		if err := s.exec(`UPDATE plans SET confirmed_count = (SELECT count(*) FROM bookings WHERE plan_id = $1 AND status = 'confirmed') WHERE id = $1`, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) connect(a, b, status string) error {
	return s.exec(`INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,$3::connection_status)`, a, b, status)
}

func (s *seeder) seedGraph() error {
	u := s.users
	// friends of the keeper (accepted): they show up in Friends feed, stories and DMs
	for _, i := range []int{0, 1, 2, 3, 4} {
		a, b := s.keeper, u[i]
		if i%2 == 1 {
			a, b = b, a
		}
		if err := s.connect(a, b, "accepted"); err != nil {
			return err
		}
	}
	// pending requests to the keeper
	for _, i := range []int{5, 6, 7} {
		if err := s.connect(u[i], s.keeper, "pending"); err != nil {
			return err
		}
	}
	// incoming waves (one super) that the keeper hasn't answered
	for k, i := range []int{8, 9, 10} {
		act := "wave"
		if k == 0 {
			act = "super"
		}
		if err := s.exec(`INSERT INTO swipes (actor_id, target_id, action) VALUES ($1,$2,$3)`, u[i], s.keeper, act); err != nil {
			return err
		}
	}
	// the rest of the city knows each other a bit
	seen := map[[2]int]bool{}
	for n := 0; n < 26; n++ {
		a, b := 11+s.rng.Intn(len(u)-11), s.rng.Intn(len(u))
		if a == b || seen[[2]int{a, b}] || seen[[2]int{b, a}] {
			continue
		}
		seen[[2]int{a, b}] = true
		if err := s.connect(u[a], u[b], "accepted"); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) seedFeed() error {
	for i, body := range postTexts {
		author := s.users[(i*3+1)%len(s.users)]
		vis := "public"
		if i%6 == 5 {
			vis = "connections"
		}
		created := s.now.Add(-time.Duration(1+i*7+s.rng.Intn(5)) * time.Hour)
		id, err := s.one(`INSERT INTO posts (author_id, body, visibility, created_at, updated_at) VALUES ($1,$2,$3,$4,$4) RETURNING id::text`, author, body, vis, created)
		if err != nil {
			return err
		}
		s.posts++
		if i%4 != 3 { // most posts carry a photo
			seed := fmt.Sprintf("post%d", i)
			if err := s.exec(`INSERT INTO post_media (post_id, media_url, thumb_url, media_type, position, width, height) VALUES ($1,$2,$3,'image',0,800,1000)`,
				id, "https://picsum.photos/seed/"+seed+"/800/1000", "https://picsum.photos/seed/"+seed+"/300/375"); err != nil {
				return err
			}
		}
		for _, j := range s.rng.Perm(len(s.users))[:2+s.rng.Intn(8)] {
			if err := s.exec(`INSERT INTO likes (post_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, s.users[j]); err != nil {
				return err
			}
		}
		for c := 0; c < s.rng.Intn(3); c++ {
			if err := s.exec(`INSERT INTO comments (post_id, author_id, body) VALUES ($1,$2,$3)`, id, s.users[s.rng.Intn(len(s.users))], commentTexts[s.rng.Intn(len(commentTexts))]); err != nil {
				return err
			}
		}
	}
	// live stories from the keeper's friends
	for k, i := range []int{0, 1, 2, 3, 4, 0, 2} {
		created := s.now.Add(-time.Duration(1+k*2) * time.Hour)
		seed := fmt.Sprintf("story%d", k)
		if err := s.exec(`INSERT INTO stories (author_id, media_url, thumb_url, media_type, caption, audience, created_at, expires_at, width, height)
			VALUES ($1,$2,$3,'image',$4,'connections',$5,$6,1080,1920)`,
			s.users[i], "https://picsum.photos/seed/"+seed+"/1080/1920", "https://picsum.photos/seed/"+seed+"/270/480",
			[]string{"Golden hour ✨", "Sunday reset", "Coffee run", "Trail views", "Dinner prep", "Rooftop night", "New find!"}[k], created, created.Add(24*time.Hour)); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) seedChats() error {
	// DMs with three of the keeper's friends; their last message is unread
	convos := [][]string{
		{"Hey! Saw you're going to the sundowner on Friday 🙌", "Yes! Are you joining?", "Definitely. First round is on me 🍻"},
		{"Loved your post about filter coffee. Any café recs in Indiranagar?", "Third Wave and Dialogues — start there!", "Perfect, meeting a friend there tomorrow ☕"},
		{"Are you free for the Sunday football game?", "Count me in. What time?", "7 AM at the turf. Bring water!"},
	}
	for c, msgs := range convos {
		friend := s.users[c]
		lo, hi := s.keeper, friend
		if lo > hi {
			lo, hi = hi, lo
		}
		room, err := s.one(`INSERT INTO chat_rooms (kind, dm_key) VALUES ('dm', $1) RETURNING id::text`, lo+":"+hi)
		if err != nil {
			return err
		}
		for _, m := range []string{s.keeper, friend} {
			if err := s.exec(`INSERT INTO chat_members (room_id, user_id) VALUES ($1,$2)`, room, m); err != nil {
				return err
			}
		}
		for k, body := range msgs {
			sender := friend
			if k%2 == 1 {
				sender = s.keeper
			}
			if err := s.exec(`INSERT INTO messages (room_id, sender_id, body, sent_at) VALUES ($1,$2,$3,$4)`, room, sender, body, s.now.Add(-time.Duration(120-c*30-k*10)*time.Minute)); err != nil {
				return err
			}
		}
	}
	// group chats for the two plans the keeper booked
	for _, pi := range []int{1, 3} {
		if plans[pi%len(plans)].approval {
			continue
		}
		room, err := s.one(`INSERT INTO chat_rooms (plan_id, kind) VALUES ($1,'plan') RETURNING id::text`, s.plans[pi])
		if err != nil {
			return err
		}
		if err := s.exec(`INSERT INTO chat_members (room_id, user_id) SELECT $1, user_id FROM bookings WHERE plan_id = $2 AND status = 'confirmed'`, room, s.plans[pi]); err != nil {
			return err
		}
		if err := s.exec(`INSERT INTO chat_members (room_id, user_id) SELECT $1, host_id FROM plans WHERE id = $2 ON CONFLICT DO NOTHING`, room, s.plans[pi]); err != nil {
			return err
		}
		if err := s.exec(`INSERT INTO messages (room_id, sender_id, body, sent_at) SELECT $1, host_id, 'Welcome everyone! Meet at the entrance 10 minutes early — look for the person with the HiveMind sticker 🐝', now() - interval '3 hours' FROM plans WHERE id = $2`, room, s.plans[pi]); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) seedCommunitiesEvents() error {
	for i, c := range communities {
		owner := s.users[(i*4+2)%len(s.users)]
		id, err := s.one(`INSERT INTO communities (name, description, city_id, owner_id, membership_type) VALUES ($1,$2,$3,$4,'public') RETURNING id::text`, c.name, c.desc, s.city, owner)
		if err != nil {
			return err
		}
		if err := s.exec(`INSERT INTO community_members (community_id, user_id, role) VALUES ($1,$2,'owner')`, id, owner); err != nil {
			return err
		}
		for _, j := range s.rng.Perm(len(s.users))[:5+s.rng.Intn(6)] {
			if s.users[j] == owner {
				continue
			}
			if err := s.exec(`INSERT INTO community_members (community_id, user_id, role) VALUES ($1,$2,'member') ON CONFLICT DO NOTHING`, id, s.users[j]); err != nil {
				return err
			}
		}
	}
	for i, e := range externalEvents {
		start := time.Date(s.now.Year(), s.now.Month(), s.now.Day(), 18, 0, 0, 0, time.Local).AddDate(0, 0, 3+i*4)
		if err := s.exec(`INSERT INTO external_events (city_id, category_id, title, description, source, source_url, venue_name, image_url, starts_at, ends_at, active)
			VALUES ($1,$2,$3,$4,'seed',$5,$6,$7,$8,$9,true)`, s.city, nullable(s.cats[e.cat]), e.title, e.desc, e.url, e.venue,
			fmt.Sprintf("https://picsum.photos/seed/event%d/800/450", i), start, start.Add(4*time.Hour)); err != nil {
			return err
		}
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// pick returns n distinct items from pool, in a deterministic-random order.
func pick(r *rand.Rand, pool []string, n int) []string {
	if n > len(pool) {
		n = len(pool)
	}
	out := make([]string, 0, n)
	for _, i := range r.Perm(len(pool))[:n] {
		out = append(out, pool[i])
	}
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "seed:", err)
	os.Exit(1)
}
