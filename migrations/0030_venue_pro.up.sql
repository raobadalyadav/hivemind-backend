-- Venue SaaS subscription tier (Phase 5, prd_docs.md §12) — reuses
-- internal/subscriptions' existing generic entitlement mechanism exactly
-- as business_pro (migration 0023) already does for hosts. No new table.
INSERT INTO subscription_products (name, price_minor, currency, interval)
VALUES ('venue_pro', 149900, 'INR', 'month');
