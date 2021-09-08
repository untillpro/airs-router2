# airs-router2
Purpose - read incoming http request, send to bus (currently implemented by `ibusnats`), read reply and send it to the client as http reponse

# Error in sections
http 200ok, but `"status":500,"errorDescription":"<error>"` is added to response stream

