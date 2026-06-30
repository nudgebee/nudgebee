import Link from 'next/link';
import PropTypes from 'prop-types';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import { colors } from 'src/utils/colors';

const CustomLink = ({ href, children, style, onClick, prop, target = '_self', secondaryText = false, openInNew = false, OpenInNewIconSx = {} }) => {
  const handleClick = (e) => {
    e.stopPropagation();
    onClick?.(e);
  };

  return (
    <Link
      passHref
      href={href}
      onClick={handleClick}
      prop={prop}
      target={openInNew ? '_blank' : target}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 'var(--ds-space-1)',
        fontSize: secondaryText ? '11px' : '13px',
        fontWeight: 'var(--ds-font-weight-regular)',
        color: colors.text.primary,
        textDecoration: 'none',
        ...style,
      }}
    >
      <span>{children}</span>

      {openInNew && (
        <OpenInNewIcon
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: 'grey',
            ...OpenInNewIconSx,
          }}
        />
      )}
    </Link>
  );
};

CustomLink.propTypes = {
  href: PropTypes.string.isRequired,
  children: PropTypes.node.isRequired,
  style: PropTypes.object,
  onClick: PropTypes.func,
  prop: PropTypes.any,
  target: PropTypes.string,
  secondaryText: PropTypes.bool,
  openInNew: PropTypes.bool,
  OpenInNewIconSx: PropTypes.object,
};

export default CustomLink;
