-- Read-only role for the Grafana Postgres datasource (queue-depth panels).
-- Mounted into /docker-entrypoint-initdb.d; runs once on a fresh data volume.
--
-- river_job does not exist yet at init time (the app applies River's
-- migrations at boot, after this file runs), so the grant is expressed as a
-- default privilege: every table the postgres role creates afterwards --
-- including river_job -- is automatically SELECT-able by grafana_ro.
-- On an existing volume apply this file by hand (see
-- docs/observability-notes.md).

CREATE ROLE grafana_ro WITH LOGIN PASSWORD 'grafana_ro_demo';

GRANT CONNECT ON DATABASE ai_incident_triage TO grafana_ro;
GRANT USAGE ON SCHEMA public TO grafana_ro;
ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public GRANT SELECT ON TABLES TO grafana_ro;
