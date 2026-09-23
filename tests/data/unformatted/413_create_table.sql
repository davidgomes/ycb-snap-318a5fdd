CREATE TABLE IF NOT EXISTS analytics.events (
  event_id INT64 NOT NULL,
  payload ARRAY<STRUCT<name STRING, values ARRAY<INT64>>>,
  user_id INT64 REFERENCES users(id, org_id),
  note VARCHAR(10) DEFAULT '',
  n INT CHECK (n > 0),
  PRIMARY KEY (event_id),
  CONSTRAINT fk FOREIGN KEY (user_id) REFERENCES users(id),
  UNIQUE (event_id, user_id),
  CHECK (event_id > 0)
)
PARTITION BY DATE(event_ts)
CLUSTER BY user_id, event_id
OPTIONS(description='daily events', labels=[('layer', 'raw')]);

CREATE TABLE films (
    code        char(5) CONSTRAINT firstkey PRIMARY KEY,
    title       varchar(40) NOT NULL,
    did         integer NOT NULL,
    date_prod   date,
    kind        varchar(10),
    len         interval hour to minute
);

create table foo as (
    aaa text,
    "bBb" int,
    ccc date
);
CREATE TABLE t1 AS SELECT * FROM range(3);
create table new_t (like old_t including all);
create table copied like source;
)))))__SQLFMT_OUTPUT__(((((
create table if not exists analytics.events (
    event_id int64 not null,
    payload array<struct<name string, values array<int64>>>,
    user_id int64 references users(id, org_id),
    note varchar(10) default '',
    n int check (n > 0),
    primary key (event_id),
    constraint fk foreign key (user_id) references users(id),
    unique (event_id, user_id),
    check (event_id > 0)
)
partition by date(event_ts)
cluster by user_id, event_id
options(description = 'daily events', labels = [('layer', 'raw')])
;

create table films (
    code char(5) constraint firstkey primary key,
    title varchar(40) not null,
    did integer not null,
    date_prod date,
    kind varchar(10),
    len interval hour to minute
)
;

create table foo as (
    aaa text,
    "bBb" int,
    ccc date
);
CREATE TABLE t1 AS SELECT * FROM range(3);
create table new_t (like old_t including all);
create table copied like source;
