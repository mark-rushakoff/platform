import React, {PureComponent} from 'react'
import classnames from 'classnames'

import Grid from 'src/shared/components/Grid'
import FancyScrollbar from 'src/shared/components/FancyScrollbar'
import DashboardEmpty from 'src/dashboards/components/DashboardEmpty'

import {Dashboard, Cell} from 'src/types/v2'

interface Props {
  dashboard: Dashboard
  onCloneCell: any
  onZoom: () => void
  setScrollTop: () => void
  inView: () => void
  inPresentationMode: boolean
  onPositionChange: (cell: Cell) => void
  onDeleteCell: (cell: Cell) => void
}

class DashboardComponent extends PureComponent<Props> {
  public render() {
    const {
      dashboard,
      onDeleteCell,
      onCloneCell,
      onPositionChange,
      inPresentationMode,
      setScrollTop,
    } = this.props

    return (
      <FancyScrollbar
        className={classnames('page-contents', {
          'presentation-mode': inPresentationMode,
        })}
        setScrollTop={setScrollTop}
      >
        <div className="dashboard container-fluid full-width">
          {dashboard.cells.length ? (
            <Grid
              isEditable={true}
              cells={dashboard.cells}
              onCloneCell={onCloneCell}
              onDeleteCell={onDeleteCell}
              onPositionChange={onPositionChange}
            />
          ) : (
            <DashboardEmpty dashboard={dashboard} />
          )}
        </div>
      </FancyScrollbar>
    )
  }
}

export default DashboardComponent
