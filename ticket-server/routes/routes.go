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
		ticketsGroup.POST("/hasura/create-ticket", controllers.CreateTicketAction)
		ticketsGroup.POST("/create-ticket", controllers.CreateTicket)
		ticketsGroup.POST("/get-ticket", controllers.GetTicket)
		ticketsGroup.POST("/search", controllers.SearchTicket)
		ticketsGroup.POST("/list", controllers.ListTickets)
		ticketsGroup.POST("/sync-tickets", controllers.SyncTickets)
		ticketsGroup.POST("/sync-configurations", controllers.SyncConfigurations)
		ticketsGroup.POST("/hasura/get-comments", controllers.GetTicketComments)
		ticketsGroup.POST("/hasura/add-comment", controllers.AddTicketComment)
		ticketsGroup.POST("/hasura/get-ticket", controllers.GetTicketByID)
		ticketsGroup.POST("/hasura/acknowledge", controllers.AcknowledgeTicket)
		ticketsGroup.POST("/hasura/escalate", controllers.EscalateTicket)
		ticketsGroup.POST("/hasura/resolve", controllers.ResolveTicket)
		ticketsGroup.POST("/hasura/update", controllers.UpdateTicket)
		ticketsGroup.POST("/hasura/transition", controllers.TransitionTicket)
		ticketsGroup.POST("/hasura/assign", controllers.AssignTicket)
		ticketsGroup.POST("/hasura/test-connection", controllers.TestConnection)
		ticketsGroup.POST("/hasura/test-connection-by-config", controllers.TestConnectionByConfig)
	}

	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
}
