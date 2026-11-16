-- Standard Classic Northwind Analytical & Relational Queries

-- Query 1: Multi-table Order Summary Join
SELECT 
    o.OrderID,
    c.CompanyName,
    e.LastName + ', ' + e.FirstName AS EmployeeName,
    o.ShipCity,
    o.ShipCountry
FROM Orders o
JOIN Customers c ON o.CustomerID = c.CustomerID
JOIN Employees e ON o.EmployeeID = e.EmployeeID
ORDER BY o.OrderID;

-- Query 2: Product Inventory and Category Aggregation
SELECT 
    c.CategoryName,
    COUNT(p.ProductID) AS ProductCount,
    SUM(p.UnitsInStock) AS TotalUnits,
    AVG(p.UnitPrice) AS AvgPrice
FROM Categories c
LEFT JOIN Products p ON c.CategoryID = p.CategoryID
GROUP BY c.CategoryName
ORDER BY c.CategoryName;

-- Query 3: Self-Join Hierarchical Employee Reporting Structure
SELECT 
    e.FirstName + ' ' + e.LastName AS Employee,
    m.FirstName + ' ' + m.LastName AS Manager
FROM Employees e
LEFT JOIN Employees m ON e.ReportsTo = m.EmployeeID
ORDER BY e.EmployeeID;

-- Query 4: Customer Order Totals with OrderDetails
SELECT 
    c.CustomerID,
    c.CompanyName,
    COUNT(DISTINCT o.OrderID) AS TotalOrders,
    SUM(od.Quantity * od.UnitPrice) AS TotalSpent
FROM Customers c
JOIN Orders o ON c.CustomerID = o.CustomerID
JOIN OrderDetails od ON o.OrderID = od.OrderID
GROUP BY c.CustomerID, c.CompanyName
ORDER BY TotalSpent DESC;
