# Test Porting Decisions

Create one Markdown file per ambiguity, conflict, intentional deviation or not-applicable decision.

Reference the decision file from the `notes` field of every affected `ports/test-port-map.csv` row. A conflict or `not-applicable` row is not complete until the record is reviewed.

Required fields:

```text
Title:
Source revision/files/tests:
Conflict or ambiguity:
Economic/API impact:
Options considered:
Decision:
Tests added/changed:
Approver:
Date:
```

Do not use this directory to bypass invariants or hide unfinished behavior.
