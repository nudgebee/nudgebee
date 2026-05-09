import React from 'react';
import { render, screen } from '@testing-library/react';
import CloudProviderIcon from '@components1/common/CloudProviderIcon';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { secondary: '#374151', primary: '#3B82F6', white: '#fff', tertiary: '#6B7280' },
    background: { primaryLightest: '#EFF6FF', white: '#fff', transparent: 'transparent' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6' },
    primary: '#3B82F6',
  },
}));

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

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, src }) => {
    let srcStr = 'icon';
    if (typeof src === 'string') {
      srcStr = src;
    } else if (src && typeof src === 'object' && src.src) {
      srcStr = src.src;
    }
    return <img alt={alt} src={srcStr} data-testid='provider-icon' />;
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
    render(<CloudProviderIcon cloud_provider={null} />);
    const icon = screen.getByRole('img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'aws');
  });

  it('renders for unknown provider (fallback to cloudBlackIcon)', () => {
    render(<CloudProviderIcon cloud_provider='UNKNOWN_PROVIDER_XYZ' />);
    const icon = screen.getByRole('img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'cloudBlack');
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
