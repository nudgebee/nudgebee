import { GithubIcon } from '@assets';
import CustomLink from '@components1/common/CustomLink';
import Datetime from '@components1/common/format/Datetime';
import CustomTable from '@components1/common/tables/CustomTable2';
import { Typography } from '@mui/material';
import { snakeToTitleCase } from 'src/utils/common';

class GithubReview {
  constructor(analysis, event) {
    this.id = 'GithubReview';
    this.icon = GithubIcon;
    this.text = 'GitHub Code Review';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.event = event;
    this.errorMessage = '';
    this.analysisData = analysis;
  }

  canRenderContent = async () => {
    if (this.analysisData?.aiData?.pr_list?.length > 0) {
      this.renderContent = true;
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderAnalysis()];
  };

  renderAnalysis = () => {
    let tableData = [];
    let automatedTableData = [];
    if (this.analysisData?.aiData?.pr_list?.length > 0) {
      tableData = this.analysisData?.aiData?.pr_list?.map((e) => {
        return [
          { component: e.created_at ? <Datetime value={e.created_at} /> : { text: '-' } },
          { text: e.title },
          {
            component: (
              <CustomLink style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }} target={'_blank'} href={e.url}>
                {e.number}
              </CustomLink>
            ),
          },
          {
            text: e?.state && snakeToTitleCase(e.state),
          },
        ];
      });
    }

    const automatedFixPr = this.analysisData?.aiData?.automated_fix_pr || {};
    if (automatedFixPr && Object.keys(automatedFixPr).length) {
      automatedTableData = [
        [
          { text: automatedFixPr.title },
          {
            component: (
              <CustomLink style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }} target={'_blank'} href={automatedFixPr.url}>
                {automatedFixPr.number}
              </CustomLink>
            ),
          },
        ],
      ];
    }

    return (
      <>
        <Typography sx={{ fontSize: '13px', color: '#6B7280', marginBottom: '12px' }}>
          AI-analyzed git blame identifying the pull requests that likely introduced this issue and any automated fix PRs created.
        </Typography>
        <Typography sx={{ fontWeight: 500 }}>The issue was introduced by the following PRs:</Typography>
        <CustomTable
          headers={['PR Created At', 'PR Title', 'PR Number', 'PR State']}
          tableData={tableData}
          onPageChange={undefined}
          rowsPerPage={tableData.length}
          totalRows={tableData.length}
        />
        {automatedTableData.length ? (
          <>
            <br />
            <Typography sx={{ fontWeight: 500 }}>The Following PRs Were Automatically Created to Address These Issues:</Typography>
            <CustomTable
              headers={['PR Title', 'PR Number']}
              tableData={automatedTableData}
              onPageChange={undefined}
              rowsPerPage={automatedTableData.length}
              totalRows={automatedTableData.length}
            />
          </>
        ) : null}
      </>
    );
  };
}

export default GithubReview;
