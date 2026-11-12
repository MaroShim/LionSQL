package seed

import (
	_ "embed"
)

// DemoSeedSQL embeds the canonical demo e-commerce and banking SQL seed script.
//
//go:embed demo_seed.sql
var DemoSeedSQL string

// DemoTables lists the tables created by DemoSeedSQL.
var DemoTables = []string{
	"customers",
	"accounts",
	"products",
	"sales",
	"sales_details",
}

// DemoProcedures lists the T-SQL stored procedures created by DemoSeedSQL.
var DemoProcedures = []string{
	"TransferFunds",
	"ProcessOrder",
}

// DemoRecordCount is the total number of initial records inserted across all tables:
// 5 (customers) + 5 (accounts) + 8 (products) + 6 (sales) + 10 (sales_details) = 34.
const DemoRecordCount = 34
