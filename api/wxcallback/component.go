package wxcallback

import (
	"io/ioutil"
	"net/http"
	"time"

	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/errno"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/log"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/wx"

	wxbase "github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/wx/base"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/db/dao"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/db/model"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

type wxCallbackComponentRecord struct {
	CreateTime int64  `json:"CreateTime"`
	InfoType   string `json:"InfoType"`
}

func componentHandler(c *gin.Context) {
	// 记录到数据库
	body, _ := ioutil.ReadAll(c.Request.Body)
	log.Infof("[callback-component] received callback, body=%s", string(body))
	var json wxCallbackComponentRecord
	if err := binding.JSON.BindBody(body, &json); err != nil {
		log.Errorf("[callback-component] bind json err: %v", err)
		c.JSON(http.StatusOK, errno.ErrInvalidParam.WithData(err.Error()))
		return
	}
	log.Infof("[callback-component] infoType=%s, createTime=%d", json.InfoType, json.CreateTime)
	r := model.WxCallbackComponentRecord{
		CreateTime:  time.Unix(json.CreateTime, 0),
		ReceiveTime: time.Now(),
		InfoType:    json.InfoType,
		PostBody:    string(body),
	}
	if json.CreateTime == 0 {
		r.CreateTime = time.Unix(1, 0)
	}
	if err := dao.AddComponentCallBackRecord(&r); err != nil {
		log.Errorf("[callback-component] db insert err: %v", err)
		c.JSON(http.StatusOK, errno.ErrSystemError.WithData(err.Error()))
		return
	}
	log.Infof("[callback-component] saved to db, processing infoType=%s", json.InfoType)

	// 处理授权相关的消息
	var err error
	switch json.InfoType {
	case "component_verify_ticket":
		err = ticketHandler(&body)
	case "authorized":
		fallthrough
	case "updateauthorized":
		err = newAuthHander(&body)
	case "unauthorized":
		err = unAuthHander(&body)
	default:
		log.Infof("[callback-component] unhandled infoType=%s", json.InfoType)
	}
	if err != nil {
		log.Errorf("[callback-component] handler error for infoType=%s: %v", json.InfoType, err)
		c.JSON(http.StatusOK, errno.ErrSystemError.WithData(err.Error()))
		return
	}

	// 转发到用户配置的地址
	var proxyOpen bool
	proxyOpen, err = proxyCallbackMsg(json.InfoType, "", "", string(body), c)
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusOK, errno.ErrSystemError.WithData(err.Error()))
		return
	}
	if !proxyOpen {
		c.String(http.StatusOK, "success")
	}
}

type ticketRecord struct {
	ComponentVerifyTicket string `json:"ComponentVerifyTicket"`
}

func ticketHandler(body *[]byte) error {
	var record ticketRecord
	if err := binding.JSON.BindBody(*body, &record); err != nil {
		log.Errorf("[callback-ticket] bind err: %v", err)
		return err
	}
	log.Infof("[callback-ticket] received ticket=%s", record.ComponentVerifyTicket)
	if err := wxbase.SetTicket(record.ComponentVerifyTicket); err != nil {
		log.Errorf("[callback-ticket] save ticket err: %v", err)
		return err
	}
	log.Info("[callback-ticket] ticket saved successfully")
	return nil
}

type newAuthRecord struct {
	CreateTime                   int64  `json:"CreateTime"`
	AuthorizerAppid              string `json:"AuthorizerAppid"`
	AuthorizationCode            string `json:"AuthorizationCode"`
	AuthorizationCodeExpiredTime int64  `json:"AuthorizationCodeExpiredTime"`
}

func newAuthHander(body *[]byte) error {
	var record newAuthRecord
	var err error
	var refreshtoken string
	var appinfo wx.AuthorizerInfoResp
	if err = binding.JSON.BindBody(*body, &record); err != nil {
		log.Errorf("[callback-auth] bind err: %v", err)
		return err
	}
	log.Infof("[callback-auth] received auth event, authorizerAppid=%s, authCode=%s, expiredTime=%d",
		record.AuthorizerAppid, record.AuthorizationCode, record.AuthorizationCodeExpiredTime)

	log.Infof("[callback-auth] calling queryAuth with authCode=%s", record.AuthorizationCode)
	if refreshtoken, err = queryAuth(record.AuthorizationCode); err != nil {
		log.Errorf("[callback-auth] queryAuth failed: %v", err)
		return err
	}
	log.Infof("[callback-auth] queryAuth success, refreshToken=%s", refreshtoken)

	log.Infof("[callback-auth] calling GetAuthorizerInfo for appid=%s", record.AuthorizerAppid)
	if err = wx.GetAuthorizerInfo(record.AuthorizerAppid, &appinfo); err != nil {
		log.Errorf("[callback-auth] GetAuthorizerInfo failed for appid=%s: %v", record.AuthorizerAppid, err)
		return err
	}
	log.Infof("[callback-auth] GetAuthorizerInfo success, nickname=%s, apptype=%d, funcinfo=%s",
		appinfo.AuthorizerInfo.NickName, appinfo.AuthorizerInfo.AppType, appinfo.AuthorizationInfo.StrFuncInfo)

	if err = dao.CreateOrUpdateAuthorizerRecord(&model.Authorizer{
		Appid:         record.AuthorizerAppid,
		AppType:       appinfo.AuthorizerInfo.AppType,
		ServiceType:   appinfo.AuthorizerInfo.ServiceType.Id,
		NickName:      appinfo.AuthorizerInfo.NickName,
		UserName:      appinfo.AuthorizerInfo.UserName,
		HeadImg:       appinfo.AuthorizerInfo.HeadImg,
		QrcodeUrl:     appinfo.AuthorizerInfo.QrcodeUrl,
		PrincipalName: appinfo.AuthorizerInfo.PrincipalName,
		RefreshToken:  refreshtoken,
		FuncInfo:      appinfo.AuthorizationInfo.StrFuncInfo,
		VerifyInfo:    appinfo.AuthorizerInfo.VerifyInfo.Id,
		AuthTime:      time.Unix(record.CreateTime, 0),
	}); err != nil {
		log.Errorf("[callback-auth] save to db failed for appid=%s: %v", record.AuthorizerAppid, err)
		return err
	}
	log.Infof("[callback-auth] authorizer record saved to db, appid=%s, nickname=%s",
		record.AuthorizerAppid, appinfo.AuthorizerInfo.NickName)
	return nil
}

type queryAuthReq struct {
	ComponentAppid    string `wx:"component_appid"`
	AuthorizationCode string `wx:"authorization_code"`
}

type authorizationInfo struct {
	AuthorizerRefreshToken string `wx:"authorizer_refresh_token"`
}
type queryAuthResp struct {
	AuthorizationInfo authorizationInfo `wx:"authorization_info"`
}

func queryAuth(authCode string) (string, error) {
	req := queryAuthReq{
		ComponentAppid:    wxbase.GetAppid(),
		AuthorizationCode: authCode,
	}
	log.Infof("[queryAuth] calling /cgi-bin/component/api_query_auth, componentAppid=%s, authCode=%s",
		wxbase.GetAppid(), authCode)
	var resp queryAuthResp
	_, body, err := wx.PostWxJsonWithComponentToken("/cgi-bin/component/api_query_auth", "", req)
	if err != nil {
		log.Errorf("[queryAuth] wx api error: %v", err)
		return "", err
	}
	log.Infof("[queryAuth] wx api response: %s", string(body))
	if err := wx.WxJson.Unmarshal(body, &resp); err != nil {
		log.Errorf("[queryAuth] unmarshal err: %v", err)
		return "", err
	}
	log.Infof("[queryAuth] success, refreshToken=%s", resp.AuthorizationInfo.AuthorizerRefreshToken)
	return resp.AuthorizationInfo.AuthorizerRefreshToken, nil
}

type unAuthRecord struct {
	CreateTime      int64  `json:"CreateTime"`
	AuthorizerAppid string `json:"AuthorizerAppid"`
}

func unAuthHander(body *[]byte) error {
	var record unAuthRecord
	var err error
	if err = binding.JSON.BindBody(*body, &record); err != nil {
		log.Errorf("[callback-unauth] bind err: %v", err)
		return err
	}
	log.Infof("[callback-unauth] received unauthorized event, authorizerAppid=%s", record.AuthorizerAppid)
	if err := dao.DelAuthorizerRecord(record.AuthorizerAppid); err != nil {
		log.Errorf("[callback-unauth] delete record failed for appid=%s: %v", record.AuthorizerAppid, err)
		return err
	}
	log.Infof("[callback-unauth] record deleted for appid=%s", record.AuthorizerAppid)
	return nil
}
