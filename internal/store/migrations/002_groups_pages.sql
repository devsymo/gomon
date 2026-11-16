-- hosts.cfg "group" headings and "page NAME Title" titles
ALTER TABLE hosts ADD COLUMN grp TEXT NOT NULL DEFAULT '';
CREATE TABLE page_titles (
    path  TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT ''
);
