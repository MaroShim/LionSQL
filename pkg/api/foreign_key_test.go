package api_test

import (
	"testing"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func setupTestServer() (*api.Server, func()) {
	eng := memengine.New()
	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)
	cleanup := func() {
		eng.Close()
	}
	return server, cleanup
}

func TestForeignKey_InsertValidation(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	// Create parent table
	_, err := server.ExecuteSQL(`CREATE TABLE departments (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL
	);`)
	if err != nil {
		t.Fatalf("failed to create departments: %v", err)
	}

	// Create child table with table-level foreign key
	_, err = server.ExecuteSQL(`CREATE TABLE employees (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL,
		dept_id INT,
		FOREIGN KEY (dept_id) REFERENCES departments(id)
	);`)
	if err != nil {
		t.Fatalf("failed to create employees: %v", err)
	}

	// Insert parent row
	_, err = server.ExecuteSQL(`INSERT INTO departments (id, name) VALUES (10, 'Engineering');`)
	if err != nil {
		t.Fatalf("failed to insert department: %v", err)
	}

	// Valid child insert
	res, err := server.ExecuteSQL(`INSERT INTO employees (id, name, dept_id) VALUES (1, 'Alice', 10);`)
	if err != nil || !res.Success {
		t.Fatalf("expected valid insert to succeed, got: %v", err)
	}

	// Child insert with NULL foreign key (allowed by ANSI MATCH SIMPLE)
	res, err = server.ExecuteSQL(`INSERT INTO employees (id, name, dept_id) VALUES (2, 'Bob', NULL);`)
	if err != nil || !res.Success {
		t.Fatalf("expected NULL foreign key insert to succeed, got: %v", err)
	}

	// Invalid child insert referencing non-existent parent
	_, err = server.ExecuteSQL(`INSERT INTO employees (id, name, dept_id) VALUES (3, 'Charlie', 999);`)
	if err == nil {
		t.Fatalf("expected foreign key violation on invalid parent reference, but insert succeeded")
	}
}

func TestForeignKey_OnDelete_Cascade(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE authors (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL
	);`)
	if err != nil {
		t.Fatalf("failed to create authors: %v", err)
	}

	// Child table with inline REFERENCES ... ON DELETE CASCADE
	_, err = server.ExecuteSQL(`CREATE TABLE books (
		id INT PRIMARY KEY,
		title VARCHAR(100) NOT NULL,
		author_id INT REFERENCES authors(id) ON DELETE CASCADE
	);`)
	if err != nil {
		t.Fatalf("failed to create books: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO authors (id, name) VALUES (1, 'Tolkien'), (2, 'Asimov');`)
	if err != nil {
		t.Fatalf("failed to insert authors: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO books (id, title, author_id) VALUES
		(101, 'The Hobbit', 1),
		(102, 'The Fellowship of the Ring', 1),
		(201, 'Foundation', 2);`)
	if err != nil {
		t.Fatalf("failed to insert books: %v", err)
	}

	// Delete author 1 -> should cascade delete books 101 and 102
	res, err := server.ExecuteSQL(`DELETE FROM authors WHERE id = 1;`)
	if err != nil || !res.Success {
		t.Fatalf("failed to delete author 1: %v", err)
	}

	// Verify books: only book 201 should remain
	selectRes, err := server.ExecuteSQL(`SELECT * FROM books;`)
	if err != nil || !selectRes.Success {
		t.Fatalf("failed to select books: %v", err)
	}
	if len(selectRes.Rows) != 1 {
		t.Fatalf("expected 1 remaining book, got %d: %+v", len(selectRes.Rows), selectRes.Rows)
	}
	if selectRes.Rows[0]["title"] != "Foundation" {
		t.Fatalf("expected 'Foundation' to remain, got: %v", selectRes.Rows[0]["title"])
	}
}

func TestForeignKey_OnDelete_SetNull(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE categories (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL
	);`)
	if err != nil {
		t.Fatalf("failed to create categories: %v", err)
	}

	// Child table with named CONSTRAINT ... ON DELETE SET NULL
	_, err = server.ExecuteSQL(`CREATE TABLE products (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL,
		category_id INT,
		CONSTRAINT fk_cat FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL
	);`)
	if err != nil {
		t.Fatalf("failed to create products: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO categories (id, name) VALUES (1, 'Electronics');`)
	if err != nil {
		t.Fatalf("failed to insert category: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO products (id, name, category_id) VALUES (10, 'Laptop', 1);`)
	if err != nil {
		t.Fatalf("failed to insert product: %v", err)
	}

	// Delete category 1 -> category_id in product 10 should be set to NULL
	res, err := server.ExecuteSQL(`DELETE FROM categories WHERE id = 1;`)
	if err != nil || !res.Success {
		t.Fatalf("failed to delete category 1: %v", err)
	}

	// Verify product 10 still exists with category_id = NULL
	selectRes, err := server.ExecuteSQL(`SELECT * FROM products WHERE id = 10;`)
	if err != nil || !selectRes.Success || len(selectRes.Rows) != 1 {
		t.Fatalf("expected product 10 to exist, got: %+v", selectRes)
	}
	catVal := selectRes.Rows[0]["category_id"]
	if catVal != nil && catVal != "" {
		t.Fatalf("expected category_id to be NULL/nil, got: %v", catVal)
	}
}

func TestForeignKey_OnDelete_Restrict(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE users (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL
	);`)
	if err != nil {
		t.Fatalf("failed to create users: %v", err)
	}

	_, err = server.ExecuteSQL(`CREATE TABLE orders (
		id INT PRIMARY KEY,
		user_id INT REFERENCES users(id) ON DELETE RESTRICT
	);`)
	if err != nil {
		t.Fatalf("failed to create orders: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO users (id, name) VALUES (1, 'Admin');`)
	if err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO orders (id, user_id) VALUES (100, 1);`)
	if err != nil {
		t.Fatalf("failed to insert order: %v", err)
	}

	// Deleting user 1 while orders exist should fail due to RESTRICT
	_, err = server.ExecuteSQL(`DELETE FROM users WHERE id = 1;`)
	if err == nil {
		t.Fatalf("expected RESTRICT foreign key error on deleting referenced user, but delete succeeded")
	}

	// Delete child row first
	_, err = server.ExecuteSQL(`DELETE FROM orders WHERE id = 100;`)
	if err != nil {
		t.Fatalf("failed to delete order: %v", err)
	}

	// Deleting user 1 should now succeed
	res, err := server.ExecuteSQL(`DELETE FROM users WHERE id = 1;`)
	if err != nil || !res.Success {
		t.Fatalf("expected deleting user 1 to succeed after child deleted, got: %v", err)
	}
}

func TestForeignKey_OnUpdate_Cascade(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE parents (
		id INT PRIMARY KEY,
		code VARCHAR(20) NOT NULL
	);`)
	if err != nil {
		t.Fatalf("failed to create parents: %v", err)
	}

	_, err = server.ExecuteSQL(`CREATE TABLE children (
		id INT PRIMARY KEY,
		parent_id INT,
		FOREIGN KEY (parent_id) REFERENCES parents(id) ON UPDATE CASCADE
	);`)
	if err != nil {
		t.Fatalf("failed to create children: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO parents (id, code) VALUES (1, 'P1');`)
	if err != nil {
		t.Fatalf("failed to insert parent: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO children (id, parent_id) VALUES (10, 1);`)
	if err != nil {
		t.Fatalf("failed to insert child: %v", err)
	}

	// Update parent id from 1 to 50
	res, err := server.ExecuteSQL(`UPDATE parents SET id = 50 WHERE id = 1;`)
	if err != nil || !res.Success {
		t.Fatalf("failed to update parent id: %v", err)
	}

	// Verify child parent_id was updated to 50
	selectRes, err := server.ExecuteSQL(`SELECT * FROM children WHERE id = 10;`)
	if err != nil || !selectRes.Success || len(selectRes.Rows) != 1 {
		t.Fatalf("failed to select child: %+v", selectRes)
	}
	pID := selectRes.Rows[0]["parent_id"]
	if pID != int64(50) && pID != 50 && pID != "50" {
		t.Fatalf("expected child parent_id to be cascaded to 50, got: %v (%T)", pID, pID)
	}
}

func TestForeignKey_DropTableProtection(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE countries (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL
	);`)
	if err != nil {
		t.Fatalf("failed to create countries: %v", err)
	}

	_, err = server.ExecuteSQL(`CREATE TABLE cities (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL,
		country_id INT REFERENCES countries(id)
	);`)
	if err != nil {
		t.Fatalf("failed to create cities: %v", err)
	}

	// Attempting to drop countries while cities references it should fail
	_, err = server.ExecuteSQL(`DROP TABLE countries;`)
	if err == nil {
		t.Fatalf("expected DROP TABLE countries to fail due to foreign key dependency, but succeeded")
	}

	// Dropping referencing table first
	_, err = server.ExecuteSQL(`DROP TABLE cities;`)
	if err != nil {
		t.Fatalf("failed to drop child table cities: %v", err)
	}

	// Dropping parent table should now succeed
	res, err := server.ExecuteSQL(`DROP TABLE countries;`)
	if err != nil || !res.Success {
		t.Fatalf("expected DROP TABLE countries to succeed, got: %v", err)
	}
}
