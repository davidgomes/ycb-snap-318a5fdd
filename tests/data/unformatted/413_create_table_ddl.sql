CREATE TABLE IF NOT EXISTS analytics.films (
    code CHAR(5) CONSTRAINT firstkey PRIMARY KEY,
    title VARCHAR(40) NOT NULL,
    payload ARRAY<STRUCT<x INT64, y STRING>>,
    rating DECIMAL(10,2) DEFAULT 1.0,
    user_id INT REFERENCES users(id),
    PRIMARY KEY (code, title),
    CONSTRAINT positive_len CHECK (code <> ''),
    UNIQUE (title),
    FOREIGN KEY (user_id) REFERENCES users(id)
)
PARTITION BY DATE(code)
CLUSTER BY title
OPTIONS(description = 'films')
;

CREATE TABLE copied AS SELECT 1;
CREATE TABLE new_one LIKE old_one;
)))))__SQLFMT_OUTPUT__(((((
create table if not exists analytics.films (
    code char(5) constraint firstkey primary key,
    title varchar(40) not null,
    payload array<struct<x int64, y string>>,
    rating decimal(10, 2) default 1.0,
    user_id int references users(id),
    primary key (code, title),
    constraint positive_len check (code <> ''),
    unique (title),
    foreign key (user_id) references users(id)
)
partition by date(code)
cluster by title
options(description = 'films')
;

CREATE TABLE copied AS SELECT 1;
CREATE TABLE new_one LIKE old_one;
