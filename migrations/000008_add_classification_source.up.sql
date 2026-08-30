-- Tracks which classifier produced the stored verdict: "llm" or "heuristic".
-- NULL until an incident is classified; reset (with the other classification
-- columns) whenever a new delivery re-enqueues classification.
ALTER TABLE incidents ADD COLUMN classification_source TEXT;
