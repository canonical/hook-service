-- PostgreSQL Multi-Database Initialization Script for Centralized Authz E2E Testing

-- Create authorization-service database and STS database
CREATE DATABASE "authorization-service";
CREATE DATABASE sts;
GRANT ALL PRIVILEGES ON DATABASE "authorization-service" TO "authorization-service";
GRANT ALL PRIVILEGES ON DATABASE sts TO "authorization-service";
ALTER DATABASE "authorization-service" OWNER TO "authorization-service";
ALTER DATABASE sts OWNER TO "authorization-service";

-- Create groups role and groups database for hook-service
DO
$do$
BEGIN
   IF NOT EXISTS (
      SELECT FROM pg_catalog.pg_roles
      WHERE  rolname = 'groups') THEN
      CREATE ROLE groups WITH LOGIN PASSWORD 'groups';
   END IF;
END
$do$;

CREATE DATABASE groups OWNER groups;
GRANT ALL PRIVILEGES ON DATABASE groups TO groups;
GRANT ALL PRIVILEGES ON DATABASE groups TO "authorization-service";
