-- Microsoft SQL Server Pubs Schema and Sample Data

CREATE TABLE publishers (
    pub_id CHAR(4) PRIMARY KEY,
    pub_name VARCHAR(40) NULL,
    city VARCHAR(20) NULL,
    state CHAR(2) NULL,
    country VARCHAR(30) NULL
);

CREATE TABLE authors (
    au_id VARCHAR(11) PRIMARY KEY,
    au_lname VARCHAR(40) NOT NULL,
    au_fname VARCHAR(20) NOT NULL,
    phone CHAR(12) DEFAULT 'UNKNOWN',
    address VARCHAR(40) NULL,
    city VARCHAR(20) NULL,
    state CHAR(2) NULL,
    zip CHAR(5) NULL,
    contract INT NOT NULL
);

CREATE TABLE titles (
    title_id VARCHAR(6) PRIMARY KEY,
    title VARCHAR(80) NOT NULL,
    type CHAR(12) DEFAULT 'UNDECIDED',
    pub_id CHAR(4) NULL,
    price DECIMAL(10,2) NULL,
    advance DECIMAL(10,2) NULL,
    royalty INT NULL,
    ytd_sales INT NULL
);

CREATE TABLE titleauthor (
    au_id VARCHAR(11) NOT NULL,
    title_id VARCHAR(6) NOT NULL,
    au_ord INT NULL,
    royaltyper INT NULL,
    PRIMARY KEY (au_id, title_id)
);

CREATE TABLE stores (
    stor_id CHAR(4) PRIMARY KEY,
    stor_name VARCHAR(40) NULL,
    stor_address VARCHAR(40) NULL,
    city VARCHAR(20) NULL,
    state CHAR(2) NULL,
    zip CHAR(5) NULL
);

CREATE TABLE sales (
    stor_id CHAR(4) NOT NULL,
    ord_num VARCHAR(20) NOT NULL,
    ord_date DATETIME NOT NULL,
    qty INT NOT NULL,
    payterms VARCHAR(12) NOT NULL,
    title_id VARCHAR(6) NOT NULL,
    PRIMARY KEY (stor_id, ord_num, title_id)
);

-- Pubs Seed Data
INSERT INTO publishers VALUES
('0736', 'New Moon Books', 'Boston', 'MA', 'USA'),
('0877', 'Binnet & Hardley', 'Washington', 'DC', 'USA'),
('1389', 'Algodata Infosystems', 'Berkeley', 'CA', 'USA');

INSERT INTO authors VALUES
('172-32-1176', 'White', 'Johnson', '408 496-7223', '10932 Bigge Rd.', 'Menlo Park', 'CA', '94025', 1),
('213-46-8915', 'Green', 'Marjorie', '415 986-7020', '309 63rd St. #411', 'Oakland', 'CA', '94618', 1),
('238-95-7766', 'Carson', 'Cheryl', '415 548-7723', '589 Darwin Ln.', 'Berkeley', 'CA', '94705', 1),
('267-41-2394', 'O''Leary', 'Michael', '408 286-2428', '22 Cleveland Av. #14', 'San Jose', 'CA', '95128', 1),
('274-80-9391', 'Straight', 'Dean', '415 834-2919', '5420 Telegraph Av.', 'Oakland', 'CA', '94609', 1),
('409-56-7008', 'Bennet', 'Abraham', '415 658-9932', '6223 Bateman St.', 'Berkeley', 'CA', '94705', 1);

INSERT INTO titles VALUES
('BU1032', 'The Busy Executive''s Database Guide', 'business', '1389', 19.99, 5000.00, 10, 4095),
('BU1111', 'Cooking with Computers: Surreptitious Flavor', 'business', '1389', 11.95, 5000.00, 10, 3876),
('MC3021', 'The Gourmet Microwave', 'mod_cook', '0877', 2.99, 15000.00, 24, 2032),
('PS2091', 'Is Anger the Enemy?', 'psychology', '0736', 10.95, 2275.00, 12, 2045),
('TC7777', 'Sushi, Anyone?', 'trad_cook', '0877', 14.99, 8000.00, 10, 4095);

INSERT INTO titleauthor VALUES
('172-32-1176', 'BU1032', 1, 100),
('213-46-8915', 'BU1111', 1, 100),
('213-46-8915', 'MC3021', 1, 100),
('238-95-7766', 'PS2091', 1, 100),
('267-41-2394', 'TC7777', 1, 100);

INSERT INTO stores VALUES
('6380', 'Eric the Read Books', '788 Catamoun St.', 'Seattle', 'WA', '98156'),
('7066', 'Barnum''s', '567 Pasadena Ave.', 'Tustin', 'CA', '92789'),
('7067', 'News & Brews', '577 First St.', 'Los Gatos', 'CA', '95032');

INSERT INTO sales VALUES
('6380', '6871', '2023-09-14', 5, 'Net 60', 'BU1032'),
('7066', 'A22649', '2023-07-24', 50, 'Net 30', 'BU1111'),
('7066', 'QA74423', '2023-09-13', 75, 'ON invoice', 'MC3021'),
('7067', 'D4482', '2023-12-19', 20, 'Net 60', 'PS2091');
