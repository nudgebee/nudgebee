-- Empty Trivy Java DB (schema version 1, the layout of
-- ghcr.io/aquasecurity/trivy-java-db:1). Trivy refuses to analyse JAR/WAR/EAR
-- files without a Java DB, and the real one is ~1 GB compressed: too big for a
-- scan Job to download. With this empty one, trivy identifies an archive from
-- the META-INF/maven/**/pom.properties inside it and matches that against the
-- vulnerability DB as usual. Only archives without pom.properties (whose
-- identity trivy would look up by SHA-1 in the real DB) go unidentified.
CREATE TABLE artifacts(id INTEGER PRIMARY KEY, group_id TEXT, artifact_id TEXT);
CREATE TABLE indices(artifact_id INTEGER, version TEXT, sha1 BLOB, archive_type TEXT,
    FOREIGN KEY (artifact_id) REFERENCES artifacts(id));
CREATE UNIQUE INDEX artifacts_idx ON artifacts(artifact_id, group_id);
CREATE INDEX indices_artifact_idx ON indices(artifact_id);
CREATE UNIQUE INDEX indices_sha1_idx ON indices(sha1);
