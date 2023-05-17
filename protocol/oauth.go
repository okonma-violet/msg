package protocol

type OAuth_ReqType byte

const (
	ReqAuthorization    OAuth_ReqType = 1
	ReqToken            OAuth_ReqType = 2
	ReqAppRegistration  OAuth_ReqType = 3
	ReqUserRegistration OAuth_ReqType = 4
)

func (rc OAuth_ReqType) String() string {
	switch rc {
	case ReqAuthorization:
		return "authorization"
	case ReqToken:
		return "token"
	case ReqAppRegistration:
		return "app_registration"
	case ReqUserRegistration:
		return "user_registration"
	}
	return ""
}

type OAuth_GrantType string

const (
	GrantAuthorizationCode OAuth_GrantType = "authorization_code"
	GrantRefreshToken      OAuth_GrantType = "refresh_token"
	GrantPassword          OAuth_GrantType = "password"
	GrantClientCredentials OAuth_GrantType = "client_credentials"
)

func (grtt OAuth_GrantType) String() string {
	return string(grtt)
}

type OAuth_Param string

const (
	ParamResponse_type OAuth_Param = "response_type"
	ParamRedirectURI   OAuth_Param = "redirect_uri"
	ParamScope         OAuth_Param = "scope"
	ParamState         OAuth_Param = "state"

	ParamCode      OAuth_Param = "code"
	ParamGrantType OAuth_Param = "grant_type"

	ParamClientID     OAuth_Param = "client_id"
	ParamClientSecret OAuth_Param = "client_secret"

	ParamUserID OAuth_Param = "user_id"

	ParamAccessToken  OAuth_Param = "access_token"
	ParamRefreshToken OAuth_Param = "refresh_token"
	ParamTokenExpires OAuth_Param = "expires_in"

	ParamLogin    OAuth_Param = "userlogin"
	ParamPassword OAuth_Param = "password"
)

func (prm OAuth_Param) String() string {
	return string(prm)
}

// type IdentityServerMessage_Headers struct {
// 	Grant string `json:"grant,omitempty"`

// 	App_Id     string `json:"appid,omitempty"`
// 	App_Secret string `json:"appsecret,omitempty"`

// 	Access_Token  string `json:"a_token,omitempty"`
// 	Refresh_Token string `json:"r_token,omitempty"`

// 	AuthCode string `json:"code,omitempty"`
// }
