import React from 'react';
import {Redirect} from '@docusaurus/router';
import useBaseUrl from '@docusaurus/useBaseUrl';

// GitHub Pages has no server-side redirects, so send the site root to the first docs page here.
function Home() {
  return <Redirect to={useBaseUrl('/what-is-devpod')} />;
}

export default Home;
