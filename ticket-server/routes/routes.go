package routes

import (
	"nudgebee/tickets-server/controllers"

	"github.com/gin-gonic/gin"
)

func InitializeRoutes(router *gin.Engine) {
	ticketsGroup := router.Group("/tickets")
	{
		ticketsGroup.POST("/add-configuration", controllers.AddTicketConfiguration)
		ticketsGroup.POST("/create-meta", controllers.GetIssueCreationTemplate)
		ticketsGroup.POST("/query", controllers.QueryIssueFieldDetails)
		ticketsGroup.POST("/rpc/create-ticket", controllers.CreateTicketAction)
		ticketsGroup.POST("/create-ticket", controllers.CreateTicket)
		ticketsGroup.POST("/get-ticket", controllers.GetTicket)
		ticketsGroup.POST("/search", controllers.SearchTicket)
		ticketsGroup.POST("/list", controllers.ListTickets)
		ticketsGroup.POST("/sync-tickets", controllers.SyncTickets)
		ticketsGroup.POST("/sync-configurations", controllers.SyncConfigurations)
		ticketsGroup.POST("/rpc/get-comments", controllers.GetTicketComments)
		ticketsGroup.POST("/rpc/add-comment", controllers.AddTicketComment)
		ticketsGroup.POST("/rpc/get-ticket", controllers.GetTicketByID)
		ticketsGroup.POST("/rpc/acknowledge", controllers.AcknowledgeTicket)
		ticketsGroup.POST("/rpc/escalate", controllers.EscalateTicket)
		ticketsGroup.POST("/rpc/resolve", controllers.ResolveTicket)
		ticketsGroup.POST("/rpc/update", controllers.UpdateTicket)
		ticketsGroup.POST("/rpc/transition", controllers.TransitionTicket)
		ticketsGroup.POST("/rpc/assign", controllers.AssignTicket)
		ticketsGroup.POST("/rpc/test-connection", controllers.TestConnection)
		ticketsGroup.POST("/rpc/test-connection-by-config", controllers.TestConnectionByConfig)
	}

	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
}
