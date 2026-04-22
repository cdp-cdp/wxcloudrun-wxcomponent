package authpage

import (
	"encoding/json"
	"net/http"

	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/errno"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/log"
	wxbase "github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/wx/base"

	"github.com/WeixinCloud/wxcloudrun-wxcomponent/db/dao"
	"github.com/gin-gonic/gin"
)

func getComponentInfoHandler(c *gin.Context) {
	log.Infof("[componentinfo] start, appid=%s", wxbase.GetAppid())
	value := dao.GetCommKv("authinfo", "{}")
	log.Infof("[componentinfo] db authinfo=%s", value)
	var mapResult map[string]interface{}
	if err := json.Unmarshal([]byte(value), &mapResult); err != nil {
		log.Errorf("[componentinfo] unmarshal err: %v", err)
		c.JSON(http.StatusOK, errno.ErrSystemError.WithData(err.Error()))
		return
	}
	mapResult["appid"] = wxbase.GetAppid()
	log.Infof("[componentinfo] response: %v", mapResult)
	c.JSON(http.StatusOK, errno.OK.WithData(mapResult))
}
