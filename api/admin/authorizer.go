package admin

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/errno"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/log"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/wx"
	wxbase "github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/wx/base"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/db/dao"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/db/model"
	"github.com/gin-gonic/gin"
)

type getAuthorizerListReq struct {
	ComponentAppid string `wx:"component_appid"`
	Offset         int    `wx:"offset"`
	Count          int    `wx:"count"`
}

type authorizerInfo struct {
	AuthorizerAppid string `wx:"authorizer_appid"`
	RefreshToken    string `wx:"refresh_token"`
	AuthTime        int64  `wx:"auth_time"`
}
type getAuthorizerListResp struct {
	TotalCount int              `wx:"total_count"`
	List       []authorizerInfo `wx:"list"`
}

type getAuthorizerInfoResp struct {
	model.Authorizer
	RegisterType  int                       `json:"registerType"`
	AccountStatus int                       `json:"accountStatus"`
	BasicConfig   *wx.AuthorizerBasicConfig `json:"basicConfig"`
}

func pullAuthorizerListHandler(c *gin.Context) {
	log.Info("[pull-authorizer] start pulling authorizer list in background")
	go func() {
		count := 100
		offset := 0
		total := 0
		now := time.Now()
		for {
			var resp getAuthorizerListResp
			log.Infof("[pull-authorizer] calling wx api, offset=%d, count=%d", offset, count)
			if err := getAuthorizerList(offset, count, &resp); err != nil {
				log.Errorf("[pull-authorizer] getAuthorizerList error: %v", err)
				return
			}
			if total == 0 {
				total = resp.TotalCount
			}
			log.Infof("[pull-authorizer] wx api returned totalCount=%d, batch_size=%d", total, len(resp.List))
			// 插入数据库
			length := len(resp.List)
			records := make([]model.Authorizer, length)
			var wg sync.WaitGroup
			wg.Add(length)
			for i, info := range resp.List {
				log.Infof("[pull-authorizer] fetching info for appid=%s", info.AuthorizerAppid)
				go constructAuthorizerRecord(info, &records[i], &wg)
			}
			wg.Wait()
			if length > 0 {
				log.Infof("[pull-authorizer] saving %d records to db", length)
				dao.BatchCreateOrUpdateAuthorizerRecord(&records)
			}

			if length < count {
				break
			}
			offset += count
		}

		// 删除记录
		log.Infof("[pull-authorizer] clearing old records before %v", now)
		if err := dao.ClearAuthorizerRecordsBefore(now); err != nil {
			log.Errorf("[pull-authorizer] clear old records error: %v", err)
			return
		}
		log.Infof("[pull-authorizer] done, total=%d", total)
	}()
	c.JSON(http.StatusOK, errno.OK)
}

func copyAuthorizerInfo(appinfo *wx.AuthorizerInfoResp, record *model.Authorizer) {
	record.AppType = appinfo.AuthorizerInfo.AppType
	record.ServiceType = appinfo.AuthorizerInfo.ServiceType.Id
	record.NickName = appinfo.AuthorizerInfo.NickName
	record.UserName = appinfo.AuthorizerInfo.UserName
	record.HeadImg = appinfo.AuthorizerInfo.HeadImg
	record.QrcodeUrl = appinfo.AuthorizerInfo.QrcodeUrl
	record.PrincipalName = appinfo.AuthorizerInfo.PrincipalName
	record.FuncInfo = appinfo.AuthorizationInfo.StrFuncInfo
	record.VerifyInfo = appinfo.AuthorizerInfo.VerifyInfo.Id
}

func constructAuthorizerRecord(info authorizerInfo, record *model.Authorizer, wg *sync.WaitGroup) error {
	defer wg.Done()
	record.Appid = info.AuthorizerAppid
	record.AuthTime = time.Unix(info.AuthTime, 0)
	record.RefreshToken = info.RefreshToken
	var appinfo wx.AuthorizerInfoResp

	if err := wx.GetAuthorizerInfo(record.Appid, &appinfo); err != nil {
		log.Errorf("GetAuthorizerInfo fail %v", err)
		return err
	}
	copyAuthorizerInfo(&appinfo, record)
	return nil
}

func getAuthorizerList(offset, count int, resp *getAuthorizerListResp) error {
	req := getAuthorizerListReq{
		ComponentAppid: wxbase.GetAppid(),
		Offset:         offset,
		Count:          count,
	}
	_, body, err := wx.PostWxJsonWithComponentToken("/cgi-bin/component/api_get_authorizer_list", "", req)
	if err != nil {
		return err
	}
	if err := wx.WxJson.Unmarshal(body, &resp); err != nil {
		log.Errorf("Unmarshal err, %v", err)
		return err
	}
	return nil
}

func getAuthorizerListHandler(c *gin.Context) {
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil {
		c.JSON(http.StatusOK, errno.ErrInvalidParam.WithData(err.Error()))
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if err != nil {
		c.JSON(http.StatusOK, errno.ErrInvalidParam.WithData(err.Error()))
		return
	}
	if limit > 20 {
		c.JSON(http.StatusOK, errno.ErrInvalidParam)
		return
	}
	appid := c.DefaultQuery("appid", "")
	log.Infof("[authorizer-list] query params: offset=%d, limit=%d, appid=%s", offset, limit, appid)
	records, total, err := dao.GetAuthorizerRecords(appid, offset, limit)
	if err != nil {
		log.Errorf("[authorizer-list] db query error: %v", err)
		c.JSON(http.StatusOK, errno.ErrSystemError.WithData(err.Error()))
		return
	}
	log.Infof("[authorizer-list] db result: total=%d, records_count=%d", total, len(records))
	if len(records) == 0 {
		log.Info("[authorizer-list] no records in db, skip wx api calls")
		c.JSON(http.StatusOK, errno.OK.WithData(gin.H{"total": total, "records": []getAuthorizerInfoResp{}}))
		return
	}
	// 拉取最新的数据
	wg := &sync.WaitGroup{}
	wg.Add(len(records))
	resp := make([]getAuthorizerInfoResp, len(records))
	for i, record := range records {
		go func(i int, record *model.Authorizer) {
			defer wg.Done()
			resp[i].Appid = record.Appid
			resp[i].AuthTime = record.AuthTime
			resp[i].RefreshToken = record.RefreshToken

			log.Infof("[authorizer-list] calling wx.GetAuthorizerInfo for appid=%s", record.Appid)
			var appinfo wx.AuthorizerInfoResp
			if err := wx.GetAuthorizerInfo(record.Appid, &appinfo); err != nil {
				log.Errorf("[authorizer-list] GetAuthorizerInfo fail for appid=%s: %v", record.Appid, err)
				return
			}
			log.Infof("[authorizer-list] GetAuthorizerInfo success for appid=%s, nickname=%s, apptype=%d",
				record.Appid, appinfo.AuthorizerInfo.NickName, appinfo.AuthorizerInfo.AppType)
			copyAuthorizerInfo(&appinfo, &resp[i].Authorizer)
			resp[i].RegisterType = appinfo.AuthorizerInfo.RegisterType
			resp[i].AccountStatus = appinfo.AuthorizerInfo.AccountStatus
			resp[i].BasicConfig = appinfo.AuthorizerInfo.BasicConfig
		}(i, record)
	}
	wg.Wait()

	// 异步更新数据库
	go func(oldRecords []*model.Authorizer, newRecords *[]getAuthorizerInfoResp) {
		var updateRecords []model.Authorizer
		for i, newRecord := range *newRecords {
			newRecord.ID = oldRecords[i].ID
			if *oldRecords[i] != newRecord.Authorizer {
				updateRecords = append(updateRecords, newRecord.Authorizer)
			}
		}
		if len(updateRecords) != 0 {
			log.Info("update records: ", updateRecords)
			dao.BatchCreateOrUpdateAuthorizerRecord(&updateRecords)
		} else {
			log.Info("no update")
		}
	}(records, &resp)
	c.JSON(http.StatusOK, errno.OK.WithData(gin.H{"total": total, "records": resp}))
}
