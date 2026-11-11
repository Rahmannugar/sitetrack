CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE TABLE sitetrack.material_categories (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 100 AND name=btrim(name)),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    deleted_at timestamptz
);
CREATE UNIQUE INDEX material_categories_name_unique ON sitetrack.material_categories(lower(name)) WHERE deleted_at IS NULL;
CREATE INDEX material_categories_page_idx ON sitetrack.material_categories(lower(name),id) WHERE deleted_at IS NULL;
CREATE INDEX material_categories_search_idx ON sitetrack.material_categories USING gin(lower(name) public.gin_trgm_ops) WHERE deleted_at IS NULL;

CREATE TABLE sitetrack.materials (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 150 AND name=btrim(name)),
    category_id uuid NOT NULL REFERENCES sitetrack.material_categories(id),
    unit text NOT NULL CHECK (char_length(unit) BETWEEN 1 AND 24 AND unit=btrim(unit)),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    deleted_at timestamptz
);
CREATE UNIQUE INDEX materials_name_category_unit_unique ON sitetrack.materials(lower(name),category_id,unit) WHERE deleted_at IS NULL;
CREATE INDEX materials_page_idx ON sitetrack.materials(lower(name),id) WHERE deleted_at IS NULL;
CREATE INDEX materials_category_page_idx ON sitetrack.materials(category_id,lower(name),id) WHERE deleted_at IS NULL;
CREATE INDEX materials_category_history_idx ON sitetrack.materials(category_id);
CREATE INDEX materials_search_idx ON sitetrack.materials USING gin(lower(name) public.gin_trgm_ops) WHERE deleted_at IS NULL;

---- create above / drop below ----

DROP TABLE sitetrack.materials;
DROP TABLE sitetrack.material_categories;
