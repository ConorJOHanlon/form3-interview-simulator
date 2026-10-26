package scheme

const (
	statusAccepted = "ACCEPTED"
	statusRejected = "REJECTED"
)

// response is a scheme reply, sent on the wire as "RESPONSE|<status>|<reason>".
type response struct {
	status string
	reason string
}

func (r response) String() string {
	return "RESPONSE|" + r.status + "|" + r.reason
}

var (
	responseAccepted       = response{status: statusAccepted, reason: "Transaction processed"}
	responseInvalidRequest = response{status: statusRejected, reason: "Invalid request"}
	responseInvalidAmount  = response{status: statusRejected, reason: "Invalid amount"}
)
