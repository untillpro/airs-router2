# airs-router2

# Response format
- ibus.SendRequest is called
- error returned -> {500, `<error>`}
- nil response -> {500, `nil response from bus`}
- nil chunks -> {ibus.Reponse.Code, ibus.Response.Data}
- not nil chunks -> sectioned JSON response 
  - no errors -> {200, `{"sections"[{"type":"sec","path":["2"],"element":{"id1":{"fld1":"val}}}],"status":200}`
  - chunked error, has sent sections -> {200, `{"sections"[{"type":"sec","path":["2"],"element":{"id1":{"fld1":"val}}}],"status":500,"errorDescription":"<error>"}`
  - chunked error, no sections -> {200, `{"status":500,"errorDescription":"<error>"}`