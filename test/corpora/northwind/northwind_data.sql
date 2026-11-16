-- Northwind Sample Data

INSERT INTO Categories (CategoryName, Description) VALUES
('Beverages', 'Soft drinks, coffees, teas, beers, and ales'),
('Condiments', 'Sweet and savory sauces, relishes, spreads, and seasonings'),
('Confections', 'Desserts, candies, and sweet breads'),
('Dairy Products', 'Cheeses'),
('Grains/Cereals', 'Breads, crackers, pasta, and cereal'),
('Meat/Poultry', 'Prepared meats'),
('Produce', 'Dried fruit and bean curd'),
('Seafood', 'Seaweed and fish');

INSERT INTO Customers (CustomerID, CompanyName, ContactName, ContactTitle, Address, City, Country, Phone) VALUES
('ALFKI', 'Alfreds Futterkiste', 'Maria Anders', 'Sales Representative', 'Obere Str. 57', 'Berlin', 'Germany', '030-0074321'),
('ANATR', 'Ana Trujillo Emparedados y helados', 'Ana Trujillo', 'Owner', 'Avda. de la Constitucion 2222', 'Mexico D.F.', 'Mexico', '(5) 555-4729'),
('ANTON', 'Antonio Moreno Taqueria', 'Antonio Moreno', 'Owner', 'Mataderos  2312', 'Mexico D.F.', 'Mexico', '(5) 555-3932'),
('BERGS', 'Berglunds snabbkop', 'Christina Berglund', 'Order Administrator', 'Berguvsvagen  8', 'Lulea', 'Sweden', '0921-12 34 65'),
('BLAUS', 'Blauer See Delikatessen', 'Hanna Moos', 'Sales Representative', 'Forsterstr. 57', 'Mannheim', 'Germany', '0621-08460'),
('BOLID', 'Bolido Comidas preparadas', 'Martin Sommer', 'Owner', 'C/ Araquil, 67', 'Madrid', 'Spain', '(91) 555 22 82'),
('BONAP', 'Bon app', 'Laurence Lebihan', 'Owner', '12, rue des Bouchers', 'Marseille', 'France', '91.24.45.40'),
('BOTTM', 'Bottom-Dollar Markets', 'Elizabeth Lincoln', 'Accounting Manager', '23 Tsawassen Blvd.', 'Tsawassen', 'Canada', '(604) 555-4729');

INSERT INTO Employees (LastName, FirstName, Title, ReportsTo) VALUES
('Davolio', 'Nancy', 'Sales Representative', 2),
('Fuller', 'Andrew', 'Vice President, Sales', NULL),
('Leverling', 'Janet', 'Sales Representative', 2),
('Peacock', 'Margaret', 'Sales Representative', 2),
('Buchanan', 'Steven', 'Sales Manager', 2),
('Suyama', 'Michael', 'Sales Representative', 5),
('King', 'Robert', 'Sales Representative', 5),
('Callahan', 'Laura', 'Inside Sales Coordinator', 2),
('Dodsworth', 'Anne', 'Sales Representative', 5);

INSERT INTO Shippers (CompanyName, Phone) VALUES
('Speedy Express', '(503) 555-9831'),
('United Package', '(503) 555-3199'),
('Federal Shipping', '(503) 555-9931');

INSERT INTO Suppliers (CompanyName, ContactName, City, Country) VALUES
('Exotic Liquids', 'Charlotte Cooper', 'London', 'UK'),
('New Orleans Cajun Delights', 'Shelley Burke', 'New Orleans', 'USA'),
('Grandma Kellys Homestead', 'Regina Murphy', 'Ann Arbor', 'USA'),
('Tokyo Traders', 'Yoshi Nagase', 'Tokyo', 'Japan'),
('Cooperativa de Quesos Las Cabras', 'Antonio del Valle Saavedra', 'Oviedo', 'Spain');

INSERT INTO Products (ProductName, SupplierID, CategoryID, UnitPrice, UnitsInStock, Discontinued) VALUES
('Chai', 1, 1, 18.00, 39, 0),
('Chang', 1, 1, 19.00, 17, 0),
('Aniseed Syrup', 1, 2, 10.00, 13, 0),
('Chef Antons Cajun Seasoning', 2, 2, 22.00, 53, 0),
('Chef Antons Gumbo Mix', 2, 2, 21.35, 0, 1),
('Grandmas Boysenberry Spread', 3, 2, 25.00, 120, 0),
('Uncle Bobs Organic Dried Pears', 3, 7, 30.00, 15, 0),
('Northwoods Cranberry Sauce', 3, 2, 40.00, 6, 0),
('Mishi Kobe Niku', 4, 6, 97.00, 29, 1),
('Ikura', 4, 8, 31.00, 31, 0),
('Queso Cabrales', 5, 4, 21.00, 22, 0),
('Queso Manchego La Pastora', 5, 4, 38.00, 86, 0);

INSERT INTO Orders (CustomerID, EmployeeID, OrderDate, ShipVia, Freight, ShipCity, ShipCountry) VALUES
('ALFKI', 1, '2023-01-15', 1, 29.46, 'Berlin', 'Germany'),
('ANATR', 3, '2023-01-16', 2, 11.61, 'Mexico D.F.', 'Mexico'),
('ANTON', 4, '2023-01-20', 1, 65.83, 'Mexico D.F.', 'Mexico'),
('BERGS', 1, '2023-02-01', 3, 41.34, 'Lulea', 'Sweden'),
('BLAUS', 3, '2023-02-10', 2, 51.30, 'Mannheim', 'Germany'),
('BOLID', 4, '2023-02-15', 1, 58.17, 'Madrid', 'Spain'),
('BONAP', 1, '2023-02-17', 2, 22.98, 'Marseille', 'France'),
('BOTTM', 4, '2023-02-22', 3, 148.33, 'Tsawassen', 'Canada');

INSERT INTO OrderDetails (OrderID, ProductID, UnitPrice, Quantity, Discount) VALUES
(10001, 1, 18.00, 10, 0.0),
(10001, 2, 19.00, 5, 0.05),
(10002, 3, 10.00, 20, 0.0),
(10003, 4, 22.00, 15, 0.1),
(10003, 5, 21.35, 10, 0.0),
(10004, 6, 25.00, 40, 0.0),
(10004, 7, 30.00, 12, 0.05),
(10005, 1, 18.00, 25, 0.0),
(10006, 11, 21.00, 30, 0.0),
(10007, 10, 31.00, 18, 0.1),
(10008, 12, 38.00, 50, 0.05);
