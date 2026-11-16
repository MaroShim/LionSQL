package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestWikiJS_KnexAndPostgresCompatibility(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Drizzle / Knex Migration tracking table
	_, err := coord.ExecuteSQL(`
		CREATE TABLE "knex_migrations" (
			"id" INT IDENTITY(1,1) PRIMARY KEY,
			"name" VARCHAR(255) NOT NULL,
			"batch" INT NOT NULL,
			"migration_time" DATETIME
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE knex_migrations failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`
		CREATE TABLE "knex_migrations_lock" (
			"index" INT PRIMARY KEY,
			"is_locked" INT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE knex_migrations_lock failed: %v", err)
	}

	// 2. Core Wiki.js Tables (users, pages, pageHistory, navigation)
	_, err = coord.ExecuteSQL(`
		CREATE TABLE "wiki_users" (
			"id" INT IDENTITY(1,1) PRIMARY KEY,
			"email" VARCHAR(255) NOT NULL,
			"name" VARCHAR(255) NOT NULL,
			"providerKey" VARCHAR(50) DEFAULT 'local',
			"isActive" INT DEFAULT 1
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE wiki_users failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`
		CREATE TABLE "wiki_pages" (
			"id" INT IDENTITY(1,1) PRIMARY KEY,
			"path" VARCHAR(1000) NOT NULL,
			"title" VARCHAR(255) NOT NULL,
			"description" VARCHAR(500),
			"content" VARCHAR(4000) NOT NULL,
			"render" VARCHAR(4000),
			"locale" VARCHAR(10) DEFAULT 'en',
			"isPublished" INT DEFAULT 1,
			"authorId" INT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE wiki_pages failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`
		CREATE TABLE "wiki_navigation" (
			"id" INT IDENTITY(1,1) PRIMARY KEY,
			"key" VARCHAR(50) NOT NULL,
			"config" VARCHAR(2000) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE wiki_navigation failed: %v", err)
	}

	// 3. Insert user & page
	_, err = coord.ExecuteSQL(`INSERT INTO "wiki_users" ("email", "name", "isActive") VALUES ('admin@wiki.js', 'Wiki Admin', 1);`)
	if err != nil {
		t.Fatalf("INSERT wiki_users failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`
		INSERT INTO "wiki_pages" ("path", "title", "description", "content", "render", "locale", "authorId")
		VALUES ('home', 'Welcome to Wiki.js', 'Default home page', '# Welcome\nThis is Wiki.js running on LionSQL.', '<h1>Welcome</h1><p>This is Wiki.js running on LionSQL.</p>', 'en', 1);
	`)
	if err != nil {
		t.Fatalf("INSERT wiki_pages failed: %v", err)
	}

	// 4. Query page by path with author join (Standard Wiki.js view query)
	res, err := coord.ExecuteSQL(`
		SELECT p."id", p."path", p."title", p."description", u."name" AS "authorName"
		FROM "wiki_pages" p
		JOIN "wiki_users" u ON p."authorId" = u."id"
		WHERE p."path" = 'home' AND p."locale" = 'en';
	`)
	if err != nil {
		t.Fatalf("SELECT page by path failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 page row, got %d", len(res.Rows))
	}
	if res.Rows[0]["path"] != "home" || res.Rows[0]["authorName"] != "Wiki Admin" {
		t.Errorf("unexpected page query result: %v", res.Rows[0])
	}

	// 5. Update page content (Revision edit workflow)
	_, err = coord.ExecuteSQL(`
		UPDATE "wiki_pages"
		SET "title" = 'LionSQL Powered Wiki.js', "content" = '# Updated Content'
		WHERE "path" = 'home' AND "locale" = 'en';
	`)
	if err != nil {
		t.Fatalf("UPDATE wiki_pages failed: %v", err)
	}

	res, err = coord.ExecuteSQL(`SELECT "title" FROM "wiki_pages" WHERE "path" = 'home';`)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("Query updated title failed: %v", err)
	}
	if res.Rows[0]["title"] != "LionSQL Powered Wiki.js" {
		t.Errorf("expected updated title, got %v", res.Rows[0]["title"])
	}
}
