-- Only removes seeds nothing references (plans/communities keep theirs).
DELETE FROM categories c WHERE c.slug = lower(c.name)
  AND c.name IN ('Food','Coffee','Sports','Fitness','Travel','Music','Movies','Gaming','Books','Technology','Startups','Art','Photography','Adventure','Networking')
  AND NOT EXISTS (SELECT 1 FROM plans p WHERE p.category_id = c.id)
  AND NOT EXISTS (SELECT 1 FROM communities m WHERE m.category_id = c.id);
DELETE FROM interests WHERE name IN ('Food','Coffee','Sports','Fitness','Travel','Music','Movies','Gaming','Books','Technology','Startups','Art','Photography','Adventure','Networking');
