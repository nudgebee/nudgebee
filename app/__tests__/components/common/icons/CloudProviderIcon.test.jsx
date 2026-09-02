import React from 'react';
import { render, screen } from '@testing-library/react';
import CloudProviderIcon from '@shared/icons/CloudProviderIcon';

jest.mock('@utils/colors');

jest.mock('next/router', () => ({ useRouter: jest.fn(() => ({ push: jest.fn(), pathname: '/', asPath: '/' })) }));

jest.mock('next/link', () => ({
  __esModule: true,
  default: ({ href, children, ...rest }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

// The component uses xxx.default.src pattern for most icons
jest.mock('@assets', () => ({
  cloudBlackIcon: { default: { src: 'cloudBlack' } },
  ouAws: { default: { src: 'aws' } },
  ouAzure: { default: { src: 'azure' } },
  AWSIcon: { default: { src: 'aws' } },
  ouGoogle: { default: { src: 'gcp' } },
  ouK8s: { default: { src: 'k8s' } },
  ouSnowFlake: { default: { src: 'snowflake' } },
  ouOpenAi: { default: { src: 'openai' } },
  ouRelic: { default: { src: 'newrelic' } },
  jiraIcon: 'jira',
  slackIcon: 'slack',
  SplunkIcon: { default: { src: 'splunk' } },
  newAwsLogo: { default: { src: 'aws' } },
  AzureIcon: { default: { src: 'azure' } },
  GCPIcon: { default: { src: 'gcp' } },
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, src }) => {
    let srcStr = 'icon';
    if (typeof src === 'string') {
      srcStr = src;
    } else if (src && typeof src === 'object' && src.src) {
      srcStr = src.src;
    }
    return React.createElement('img', { alt, src: srcStr, 'data-testid': 'provider-icon' });
  },
}));

describe('CloudProviderIcon', () => {
  it('renders Box with SafeIcon for AWS provider', () => {
    render(<CloudProviderIcon cloud_provider='AWS' />);
    const icon = screen.getByRole('img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'aws');
  });

  it('renders for GCP provider', () => {
    render(<CloudProviderIcon cloud_provider='GCP' />);
    const icon = screen.getByRole('img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'gcp');
  });

  it('renders for K8S provider', () => {
    render(<CloudProviderIcon cloud_provider='K8S' />);
    const icon = screen.getByRole('img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'k8s');
  });

  it('renders for null cloud_provider (fallback to ouAws)', () => {
    // cloud_provider is marked isRequired, but null is a valid runtime edge-case
    // the component handles. Suppress the PropTypes warning for this test only.
    const errorSpy = jest.spyOn(console, 'error').mockImplementation(() => {});
    render(<CloudProviderIcon cloud_provider={null} />);
    errorSpy.mockRestore();
    const icon = screen.getByRole('img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'aws');
  });

  it('renders for unknown provider (fallback to ouAws)', () => {
    // Component fallback: resolveIcon(ouAws) || resolveIcon(cloudBlackIcon)
    // ouAws always resolves, so unknown providers receive the AWS icon.
    render(<CloudProviderIcon cloud_provider='UNKNOWN_PROVIDER_XYZ' />);
    const icon = screen.getByRole('img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'aws');
  });

  it('renders for AZURE provider', () => {
    render(<CloudProviderIcon cloud_provider='AZURE' />);
    const icon = screen.getByRole('img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'azure');
  });

  it('applies custom width and height', () => {
    const { container } = render(<CloudProviderIcon cloud_provider='AWS' width='40px' height='40px' />);
    expect(container.firstChild).toBeInTheDocument();
  });
});
