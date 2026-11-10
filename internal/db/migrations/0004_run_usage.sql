-- Token usage of LLM calls, and the cost estimated from the list price at the time.
ALTER TABLE identification_runs ADD COLUMN input_tokens INTEGER;
ALTER TABLE identification_runs ADD COLUMN output_tokens INTEGER;
ALTER TABLE identification_runs ADD COLUMN cost_usd REAL;
