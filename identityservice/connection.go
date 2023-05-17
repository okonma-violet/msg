package identityserver

import (
	"encoding/json"
	"errors"

	"github.com/okonma-violet/msg/protocol"
	"github.com/okonma-violet/services/logs/logger"

	"github.com/big-larry/suckutils"
	"github.com/okonma-violet/connector"
)

type Sender interface {
	Send(message []byte) error
}

type conninfo struct {
	conn *connector.EpollConnector[protocol.AppMessage, *protocol.AppMessage]
	l    logger.Logger
}

type oauth_hdrspart struct {
	Req_Type protocol.OAuth_ReqType `json:"reqtype"`
}

type authreq_hdrs struct {
	Params struct {
		RespType    string   `json:"response_type"`
		ClientID    string   `json:"client_id"`
		RedirectURI string   `json:"redirect_uri"`
		Scope       []string `json:"scope"`
		State       string   `json:"state"`
	} `json:"params"`
}

type tokenreq_hdrs struct {
	Params struct {
		GrantType    protocol.OAuth_GrantType `json:"grant_type"`
		Code         string                   `json:"code"`
		RedirectURI  string                   `json:"redirect_uri"`
		ClientID     string                   `json:"client_id"`
		ClientSecret string                   `json:"client_secret"`
	} `json:"params"`
}

func (ci *conninfo) Handle(message *protocol.AppMessage) (err error) {

	response := &protocol.AppMessage{Type: protocol.TypeOAuthData, ConnectionUID: message.ConnectionUID, Timestamp: message.Timestamp}
	var hdrsPart oauth_hdrspart
	if message.Type != protocol.TypeOAuthData {
		ci.l.Error("Handle/Unmarshal", errors.New(suckutils.ConcatTwo("unsupportable message type: ", message.Type.String())))
		goto bad_req
	}
	if len(message.Headers) == 0 {
		ci.l.Error("Handle/Unmarshal", errors.New("empty headers"))
		goto bad_req
	}
	if err = json.Unmarshal(message.Headers, &hdrsPart); err != nil {
		ci.l.Error("Handle/Unmarshal", err)
		goto bad_req
	}

	switch hdrsPart.Req_Type {
	case protocol.ReqAuthorization:
		hdrs := &authreq_hdrs{}
		if err = json.Unmarshal(message.Headers, hdrs); err != nil {
			ci.l.Error("Handle/Unmarshal", err)
			goto bad_req
		}
		if hdrs.Params.RespType != "code" {
			ci.l.Error("Handle", errors.New(suckutils.ConcatTwo("unsupportable response type: ", hdrs.Params.RespType)))
			goto bad_req
		}
		if len(message.Body) > 2 {
			if len(message.Body) >= 1+int(message.Body[0]) {
				if len(message.Body) == 2+int(message.Body[0])+int(message.Body[int(message.Body[0])+1]) {
					if grant, errCode := handler.Handle_Auth_Request(string(message.Body[1:1+int(message.Body[0])]), string(message.Body[2+int(message.Body[0]):]), hdrs.Params.ClientID, hdrs.Params.RedirectURI, hdrs.Params.Scope); errCode == 0 {
						if response.Headers, err = json.Marshal(struct {
							Redirect string `json:"redirect_uri"`
							Code     string `json:"code"`
							State    string `json:"state"`
						}{Redirect: hdrs.Params.RedirectURI, Code: grant, State: hdrs.Params.State}); err != nil {
							ci.l.Error("Handle/Marshal", err)
							response.Type = protocol.TypeError
							response.Body = []byte{protocol.ErrCodeInternalServerError.Byte()}
						} else {
							response.Type = protocol.TypeOAuthData
						}
					} else {
						response.Type = protocol.TypeError
						response.Body = []byte{errCode.Byte()}
					}
					goto sending
				}
			}
		}
	case protocol.ReqToken:
		hdrs := &tokenreq_hdrs{}
		if err = json.Unmarshal(message.Headers, hdrs); err != nil {
			ci.l.Error("Handle/Unmarshal", err)
			goto bad_req
		}
		if hdrs.Params.GrantType != protocol.GrantAuthorizationCode {
			ci.l.Error("Handle", errors.New(suckutils.ConcatTwo("unsupportable grant type: ", hdrs.Params.GrantType.String())))
			goto bad_req
		}
		if atoken, rtoken, uid, expires, errCode := handler.Handle_Token_Request(hdrs.Params.ClientID, hdrs.Params.ClientSecret, hdrs.Params.Code, hdrs.Params.RedirectURI); errCode == 0 {
			if response.Headers, err = json.Marshal(struct {
				AToken  string `json:"access_token"`
				RToken  string `json:"refresh_token"`
				Expires int64  `json:"expires_in"`
				UserID  string `json:"user_id"`
			}{AToken: atoken, RToken: rtoken, Expires: expires, UserID: uid}); err != nil {
				ci.l.Error("Handle/Marshal", err)
				response.Type = protocol.TypeError
				response.Body = []byte{protocol.ErrCodeInternalServerError.Byte()}
			} else {
				response.Type = protocol.TypeOAuthData
			}
		} else {
			response.Type = protocol.TypeError
			response.Body = []byte{errCode.Byte()}
		}
		goto sending
	case protocol.ReqAppRegistration:

	case protocol.ReqUserRegistration:

	default:
		ci.l.Error("Handle", errors.New("unknown req type"))
	}
bad_req:
	response.Type = protocol.TypeError
	response.Body = []byte{protocol.ErrCodeBadRequest.Byte()}
sending:
	resp_encoded, err := response.Byte()
	if err != nil {
		ci.l.Error("Encode response", err)
		resp_encoded, _ = (&protocol.AppMessage{Type: protocol.TypeError, Body: []byte{protocol.ErrCodeInternalServerError.Byte()}}).Byte()
	}
	if err = ci.conn.Send(resp_encoded); err != nil {
		ci.l.Error("Send", err)
	}
	return
}

func (ci *conninfo) HandleClose(reason error) {
	if reason != nil {
		ci.l.Warning("Conn", suckutils.ConcatTwo("closed, reason: ", reason.Error()))
	} else {
		ci.l.Warning("Conn", "closed, no reason specified")
	}
}
