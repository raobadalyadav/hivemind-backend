-- flow.md §4 interest categories; ON CONFLICT so re-running against an
-- already-seeded database is harmless.
INSERT INTO categories (name, slug)
SELECT n, lower(n) FROM unnest(ARRAY['Food','Coffee','Sports','Fitness','Travel','Music','Movies','Gaming',
    'Books','Technology','Startups','Art','Photography','Adventure','Networking']) AS n
ON CONFLICT DO NOTHING;
INSERT INTO interests (name)
SELECT unnest(ARRAY['Food','Coffee','Sports','Fitness','Travel','Music','Movies','Gaming',
    'Books','Technology','Startups','Art','Photography','Adventure','Networking'])
ON CONFLICT DO NOTHING;
