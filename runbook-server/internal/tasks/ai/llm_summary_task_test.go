package ai

import (
	"log/slog"
	// "nudgebee/runbook/internal/tasks/types" // Removed unused import
	"nudgebee/runbook/internal/tasks/testutils" // Updated import path
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

const testSummaryText = `The invention of the movable-type printing press by Johannes Gutenberg in the mid-15th century stands as one of the most transformative technological innovations in human history. Prior to its widespread adoption, the creation and dissemination of written knowledge was a painstaking, slow, and expensive process dominated by the labor of scribes and monasteries. Books were rare, precious commodities accessible almost exclusively to the clergy, the aristocracy, and wealthy institutions. The printing press dramatically shattered this restrictive intellectual barrier, acting as the ultimate catalyst for the modern era.

The immediate impact was a revolutionary democratization of knowledge. Suddenly, the cost of producing a book plummeted, allowing for mass production of pamphlets, texts, and scientific papers. This accessibility was crucial for fueling the Renaissance, which was characterized by a renewed interest in classical learning and humanistic thought. Scholars could now rapidly share and critique ideas across geographical boundaries, leading to an exponential increase in the rate of discovery and innovation. Furthermore, the standardization of texts reduced errors inherent in manual copying, creating a shared, reliable foundation for academic and legal discourse.

Perhaps the most profound influence of the press was its role in enabling the Protestant Reformation. Martin Luther successfully utilized the new technology to spread his ninety-five theses and other theological critiques rapidly throughout Europe. Where previous heretical ideas had been quickly suppressed, the sheer volume of printed material made it impossible for the Catholic Church to control the narrative. The ability of the common person to read the Bible in their native vernacular—rather than Latin—significantly eroded the central authority of the priesthood and empowered individual interpretation of scripture. This technological boost to religious dissent fundamentally reshaped the geopolitical landscape of Europe.

Shaping Modern Culture and Politics
Beyond religion and scholarship, the printing press was instrumental in the formation of modern national identities. The move from Latin to standardized, vernacular languages was strongly reinforced by the print medium. As more texts were printed in languages like English, French, and German, it helped to solidify regional dialects into national languages, creating linguistic unity and a shared sense of cultural belonging among distant populations.

In the realm of politics, the press became the foundational tool for public opinion and political organization. It enabled the distribution of news, commentary, and manifestos, paving the way for the development of newspapers and journals—the first forms of mass media. Ideas that underpinned the Enlightenment, such as human rights, liberty, and self-governance, were circulated widely through print, providing the intellectual firepower for subsequent political revolutions in America and France. In essence, the press transformed society from one where information flowed top-down, controlled by elites, to one where a bottom-up flow of ideas became increasingly possible.

In conclusion, the printing press was not merely an invention; it was an epochal engine of change. By mechanizing literacy, it decentralized power, enabled scientific and religious revolutions, formalized national languages, and laid the groundwork for modern journalism and participatory democracy. Its legacy is the very structure of the information age we inhabit today.`

func TestLLM_Summary(t *testing.T) {
	task := &LLMSummaryTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expected      any
		expectErr     bool
		expectedError string
	}{
		{
			name: "Simple Command Execution",
			params: map[string]any{
				"message": "Can you summerize following text in one sentence with 30 words. \n\n" + testSummaryText,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Clean up any potential temporary files from previous runs
			result, err := task.Execute(taskCtx, tc.params)

			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result.(map[string]any)["data"])
			}
		})
	}
}
