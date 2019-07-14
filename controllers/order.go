package controllers

import (
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/orm"
	"pyg/pyg/models"
	"strconv"
	"github.com/gomodule/redigo/redis"
	"time"
	"strings"
	"github.com/smartwalle/alipay"
)

type OrderController struct {
	beego.Controller
}

//展示订单页面
func(this*OrderController)ShowOrder(){
	//获取数据
	goodsIds := this.GetStrings("checkGoods")

	//校验数据
	if len(goodsIds) == 0 {
		this.Redirect("/user/showCart",302)
		return
	}
	//处理数据
	//获取当前用户的所有收货地址
	name := this.GetSession("name")

	o := orm.NewOrm()
	var addrs []models.Address
	o.QueryTable("Address").RelatedSel("User").Filter("User__Name",name.(string)).All(&addrs)
	this.Data["addrs"] = addrs

	conn,_ := redis.Dial("tcp","192.168.230.81:6379")

	//获取商品,获取总价和总件数
	var goods []map[string]interface{}
	var totalPrice ,totalCount int

	for _,v := range goodsIds{
		temp := make(map[string]interface{})
		id,_ := strconv.Atoi(v)
		var goodsSku models.GoodsSKU
		goodsSku.Id = id
		o.Read(&goodsSku)

		//获取商品数量
		count,_ := redis.Int(conn.Do("hget","cart_"+name.(string),id))

		//计算小计
		littlePrice := count * goodsSku.Price


		//把商品信息放到行容器
		temp["goodsSku"] = goodsSku
		temp["count"] = count
		temp["littlePrice"] = littlePrice

		totalPrice += littlePrice
		totalCount += 1

		goods = append(goods,temp)

	}

	//返回数据
	this.Data["totalPrice"] = totalPrice
	this.Data["totalCount"] = totalCount
	this.Data["truePrice"] = totalPrice + 10
	this.Data["goods"] = goods
	this.Data["goodsIds"] = goodsIds
	this.TplName = "place_order.html"
}

//提交订单
func(this*OrderController)HandlePushOrder(){
	//获取数据
	addrId,err1 := this.GetInt("addrId")
	payId,err2 := this.GetInt("payId")
	goodsIds := this.GetString("goodsIds")
	totalCount,err3 := this.GetInt("totalCount")
	totalPrice ,err4 := this.GetInt("totalPrice")



	resp := make(map[string]interface{})
	defer RespFunc(&this.Controller,resp)

	name := this.GetSession("name")
	if name == nil{
		resp["errno"] = 2
		resp["errmsg"] = "当前用户未登录"
		return
	}

	//校验数据
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || goodsIds == ""{
		resp["errno"] = 1
		resp["errmsg"] = "传输数据不完整"
		return
	}
	//处理数据
	//把数据插入到mysql数据库中
	//获取用户对象和地址对象
	o := orm.NewOrm()
	var user models.User
	user.Name = name.(string)
	o.Read(&user,"Name")

	var address models.Address
	address.Id = addrId
	o.Read(&address)

	var orderInfo models.OrderInfo

	orderInfo.User = &user
	orderInfo.Address = &address
	orderInfo.PayMethod = payId
	orderInfo.TotalCount = totalCount
	orderInfo.TotalPrice = totalPrice
	orderInfo.TransitPrice = 10
	orderInfo.OrderId = time.Now().Format("20060102150405"+strconv.Itoa(user.Id))
	//开启事务
	o.Begin()
	o.Insert(&orderInfo)

	conn,_:=redis.Dial("tcp","192.168.230.81:6379")

	defer conn.Close()
	//插入订单商品
	//goodsIds  //2  3  5
	goodsSlice:= strings.Split(goodsIds[1:len(goodsIds)-1]," ")
	for _,v := range goodsSlice{
		//插入订单商品表

		//获取商品信息
		id,_ := strconv.Atoi(v)
		var goodsSku models.GoodsSKU
		goodsSku.Id = id
		o.Read(&goodsSku)

		oldStock := goodsSku.Stock
		beego.Info("原始库存等于",oldStock)

		//获取商品数量
		count,_ := redis.Int(conn.Do("hget","cart_"+name.(string),id))

		//获取小计
		littlePrice := goodsSku.Price * count

		//插入
		var orderGoods models.OrderGoods
		orderGoods.OrderInfo = &orderInfo
		orderGoods.GoodsSKU = &goodsSku
		orderGoods.Count = count
		orderGoods.Price = littlePrice
		//插入之前需要更新商品库存和销量
		if goodsSku.Stock < count{
			resp["errno"] = 4
			resp["errmsg"] = "库存不足"
			o.Rollback()
			return
		}
		//goodsSku.Stock -= count
		//goodsSku.Sales += count

		o.Read(&goodsSku)

		qs := o.QueryTable("GoodsSKU").Filter("Id",id).Filter("Stock",oldStock)
		num,_:= qs.Update(orm.Params{"Stock":goodsSku.Stock - count,"Sales":goodsSku.Sales+count})
		if num == 0 {
			resp["errno"] = 7
			resp["errmsg"] = "购买失败，请重新排队！"
			o.Rollback()
			return
		}



		_,err := o.Insert(&orderGoods)
		if err != nil {
			resp["errno"] = 3
			resp["errmsg"] = "服务器异常"
			o.Rollback()
			return
		}
		_,err = conn.Do("hdel","cart_"+name.(string),id)
		if err != nil {
			resp["errno"] = 6
			resp["errmsg"] = "清空购物车失败"
			o.Rollback()
			return
		}

	}


	//返回数据
	o.Commit()
	resp["errno"] = 5
	resp["errmsg"] = "OK"
}

//支付
func(this*OrderController)Pay(){
	//获取数据
	orderId,err:=this.GetInt("orderId")
	if err != nil {
		this.Redirect("/user/userOrder",302)
		return
	}
	//处理数据
	o := orm.NewOrm()
	var orderInfo models.OrderInfo
	orderInfo.Id = orderId
	o.Read(&orderInfo)

	//支付


	//appId, aliPublicKey, privateKey string, isProduction bool
	publiKey := `MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAuosEOr5UqnPXwt3jHva9
r3iqipNy4c6xA9dSNX/rGLtGVcRqsJrDFV+Gg8RQsPxpqIuJ/9LbJnAbQuKRBvtb
Kk6mbWD4ppKVxehkWAMoCK5LFz3Ug8WmSFINEoHZ/TWAXXsYphbI4dMYuUtF814R
yS4btYMWhN4XVgmArRTTxCnZcPZGlJ4avRHHerXY5ChXnA2GVp/cDtBzYdfVRC+Q
o4MEmjBROE3Af8BqXNaID0RzId86JRObx+BQdDR0A1gg+/KXFG9mqF3QM5ovIljY
0QdR79vI1KfbUIHfU1P3IfxVvT+AgAlWWDTQBdHG+m1jHLooBgI1/nj7QkzmOZnw
6QIDAQAB`

	privateKey := `MIIEpAIBAAKCAQEAuosEOr5UqnPXwt3jHva9r3iqipNy4c6xA9dSNX/rGLtGVcRq
sJrDFV+Gg8RQsPxpqIuJ/9LbJnAbQuKRBvtbKk6mbWD4ppKVxehkWAMoCK5LFz3U
g8WmSFINEoHZ/TWAXXsYphbI4dMYuUtF814RyS4btYMWhN4XVgmArRTTxCnZcPZG
lJ4avRHHerXY5ChXnA2GVp/cDtBzYdfVRC+Qo4MEmjBROE3Af8BqXNaID0RzId86
JRObx+BQdDR0A1gg+/KXFG9mqF3QM5ovIljY0QdR79vI1KfbUIHfU1P3IfxVvT+A
gAlWWDTQBdHG+m1jHLooBgI1/nj7QkzmOZnw6QIDAQABAoIBAFi2igFhyKPzSXXL
zhpIn3bWfMxASQ8oG7jG6rq0pdpyHYXmThLE1ufQMQlzECjLMXhNPAikf0ItaFmL
pArc+MMK+kzkI/wblAy1cxsEDULrmJxp9CnikiyskLjvdfrcObq7MsKx7UCwAn8E
VDTj1LOHMPhGaiwv7oslI8OsNvV/XBr3z5vBW4JXtyVZJBchm1dts1YYQHPiRq/o
ENcwKTW2hx8AGjz5w4mPodvn+Mmlc95mC41Q17s6+ROjmD5XMw/qzcK4ZTPSW1+H
XzmLiJdA9d/ux6DKkZF0aypphEFasK07Dzsjl5LODmCOSyDOTG4skwa6HxcCzGRm
w2CMTHECgYEA9tVWgHf4slIDdgjab38mGZ6vfMTMi6A5EoGJoRBeefHmtDStKjgQ
0bHPRWzEcPM+ZJTRJDG5APwTb1Ra/FP0ocBh4Xsr+kD52oYRUWE7YHLzGu0C7aOp
cIJqgf8XLD4Y/g8EehG6Kzbfd7H7x/Ou394CJWgsbrHBuJBJuvWJlM0CgYEAwXh+
i1RFNrXsSuGi9FykMKiNFop6Oxl/mECSzLCXwts9iUPAbwTe30kwWreo2WLCUky9
1lCxRsbAUWTfdNPFsJrfDBPgwGNrKV5MTLDajy/t4USZ/cRQsSoWC5Yn7Ij4Q0lN
JjNHK6tHfKClYZdgtX5E4CHpegwyf0/KUJY+7I0CgYEA3OQUUkmK5UHh2QqZOHho
BztsPlL73eQXzwjfuqSkd6rUU+ZkJUkhPBdMrwtkTNRRvL803pgkwM3VMqch+XfE
j9BTh+6rb3wgXL/n1ZUXBvw3tJvwJ+xzoL0FRaqb+TrlMM8NqZQdr7ieiUZdVRYt
JChQcVtlj/ZBr8JoSQidA+0CgYBnZ9Oa/IuR1mJZE4hZOzq2lx/xsEnsVJCR+9F6
fdhfWXbmasPrkprclO23Tvp8VgCupD3C0pYt0gTwfA3DD31WCzCz79vseDbKgZAe
XVgzt9ZY1KXJsKfASVJHFxZ3oi2vKPqHNFkRyhYHUoWSR6p01uxRL07u4J4M1cS4
ldVD8QKBgQCQszE7Vl/obIVnCGySwxlmcO1DKXlCRVnFcjegvYXSkBDCC2SkAbYX
DtUz3+eZ74mr9rO3WIuj79SWhvnIdktmWY6WrxTGNR0OCdU90f2Ohr7qmUVTwsU/
2M0Byslb7u+6Lw1cS+H/y+aH84WGQyGBGdxnyLXofpBBCzepLRa5MA==
`
	client := alipay.New("2016092200569649",publiKey,privateKey,false)
	var p = alipay.TradePagePay{}
	p.NotifyURL = "http://192.168.230.81:8080/payOK"
	p.ReturnURL = "http://192.168.230.81:8080/payOK"
	p.Subject = "品优购"
	p.OutTradeNo = orderInfo.OrderId
	p.TotalAmount = strconv.Itoa(orderInfo.TotalPrice)
	p.ProductCode = "FAST_INSTANT_TRADE_PAY"
	url, err := client.TradePagePay(p)
	if err != nil {
		beego.Error("支付失败")
	}
	payUrl := url.String()
	this.Redirect(payUrl,302)
}