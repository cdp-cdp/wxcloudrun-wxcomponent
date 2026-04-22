package middleware

import (
	"fmt"
	"net/http"

	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/errno"

	"github.com/gin-gonic/gin"
)

// WXSourceMiddleWare 中间件 判断是否来源于微信
func WXSourceMiddleWare(c *gin.Context) {
	if _, ok := c.Request.Header[http.CanonicalHeaderKey("x-wx-source")]; ok {
		fmt.Println("[WXSourceMiddleWare]from wx")
		c.Next()
	} else {
		fmt.Printf("[WXSourceMiddleWare]no x-wx-source header, path=%s, method=%s, ip=%s\n",
			c.Request.URL.Path, c.Request.Method, c.ClientIP())
		// 临时放行：URL模式下微信回调不带x-wx-source头
		c.Next()
	}
}
