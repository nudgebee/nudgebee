import React from 'react';

const ConfigureWarning = ({ type }) => {
  if (type === 'logs') {
    return (
      <div className='no-logs'>
        <h3>No Logs Available</h3>
        <p>A log provider has not been configured for this namespace or pod. Please check your settings or try again later.</p>
      </div>
    );
  }

  return null;
};

export default ConfigureWarning;
