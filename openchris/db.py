import os
from contextlib import contextmanager
from functools import lru_cache

from sqlalchemy import URL, create_engine, inspect
from sqlalchemy.orm import DeclarativeBase, Session


class Base(DeclarativeBase):
    pass


@lru_cache(maxsize=4)
def _engine(url):
    options = {"pool_pre_ping": True}
    if str(url).startswith("sqlite"):
        options["connect_args"] = {"timeout": 20}
    return create_engine(url, **options)


def engine():
    if os.environ.get("DATABASE_URL"):
        return _engine(os.environ["DATABASE_URL"])
    url = URL.create(
        "postgresql+psycopg", username="openchris",
        password=os.environ["POSTGRES_PASSWORD"], host=os.getenv("DB_HOST", "db"),
        database="openchris",
    )
    return _engine(url)


@contextmanager
def db_session():
    with Session(engine(), expire_on_commit=False) as session:
        with session.begin():
            yield session


def initialize():
    from . import automation, banking, finance  # register ORM tables for all three domains

    with engine().begin() as conn:
        if conn.dialect.name == "postgresql":
            conn.exec_driver_sql("SELECT pg_advisory_xact_lock(73148610)")
        Base.metadata.create_all(conn)
        if "bank_direction" not in {column["name"] for column in inspect(conn).get_columns("entries")}:
            conn.exec_driver_sql("ALTER TABLE entries ADD COLUMN bank_direction VARCHAR(7)")
