# airs-router2
Purpose - read incoming http request, send to bus (currently implemented by `ibusnats`), read reply and send it to the client as http reponse

# Slow consumer determination
- `-skbps`
  - specifies the minimal allowed speed in kbits per second allowed to read one section element. Element cosumption by router (nats overhead + router overhead + write to http client) took more than `(sectionSize/(skbps*1000/8) + ibus.DefaultTimeout (10 seconds))` seconds -> `slow consumer` error is sent to http client and to the service side and the connection is closed. Examples:
    - `-skbps 1000`: len(elem or section+elem) 125000 bytes -> 11 seconds max, 250000 bytes -> 12 seconds max etc
    - `-skbps 100`: len(elem or section+elem) 125000 bytes -> 20 seconds max, 250000 bytes -> 30 seconds max etc

# Error in sections
http 200ok, but `"status":500,"errorDescription":"<error>"` is added to response stream

