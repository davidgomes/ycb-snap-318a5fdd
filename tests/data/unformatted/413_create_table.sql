-- source: https://www.postgresql.org/docs/current/sql-createtable.html
CREATE TABLE films (
    code        char(5) CONSTRAINT firstkey PRIMARY KEY,
    title       varchar(40) NOT NULL,
    did         integer NOT NULL,
    date_prod   date,
    kind        varchar(10),
    len         interval hour to minute
);
CREATE TABLE distributors (did integer, name varchar(40), PRIMARY KEY(did));
CREATE TABLE IF NOT EXISTS distributors (
    did     integer CHECK(did > 100),
    name    varchar(40) DEFAULT 'Luso Films',
    CONSTRAINT con1 CHECK (did > 100 AND name <> '')
);
create temporary table array_int (vector int[][]);
CREATE TABLE cinemas (
        id serial,
        name text,
        location text
) ;
create table orders (
    order_id integer primary key
    , product_no integer REFERENCES products (product_no) ON DELETE RESTRICT
    , customer_id integer
    , quantity numeric(10,2) not null default 0 check (quantity>=0)
    , CHECK (quantity in (1, 2, 3))
    , FOREIGN KEY(customer_id, product_no) REFERENCES customer_products(customer_id,product_no) ON DELETE CASCADE ON UPDATE CASCADE
    , UNIQUE(order_id, product_no)
)
;
create table comments (
    -- the id
    id int, -- inline
    body text  -- another inline

    , created_at timestamp with time zone default now()
);
)))))__SQLFMT_OUTPUT__(((((
-- source: https://www.postgresql.org/docs/current/sql-createtable.html
create table films (
    code char(5) constraint firstkey primary key,
    title varchar(40) not null,
    did integer not null,
    date_prod date,
    kind varchar(10),
    len interval hour to minute
)
;
create table distributors (
    did integer,
    name varchar(40),
    primary key (did)
)
;
create table if not exists distributors (
    did integer check (did > 100),
    name varchar(40) default 'Luso Films',
    constraint con1 check (did > 100 and name <> '')
)
;
create temporary table array_int (
    vector int[][]
)
;
create table cinemas (
    id serial,
    name text,
    location text
)
;
create table orders (
    order_id integer primary key,
    product_no integer references products(product_no) on delete restrict,
    customer_id integer,
    quantity numeric(10, 2) not null default 0 check (quantity >= 0),
    check (quantity in (1, 2, 3)),
    foreign key (customer_id, product_no)
        references customer_products(customer_id, product_no)
        on delete cascade
        on update cascade,
    unique (order_id, product_no)
)
;
create table comments (
    -- the id
    id int,  -- inline
    body text,  -- another inline
    created_at timestamp with time zone default now()
)
;
