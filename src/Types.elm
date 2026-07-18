module Types exposing (Model, Msg(..), SermonList(..), UploadState(..))

import Api exposing (Sermon)
import File exposing (File)
import Http
import Time


type SermonList
    = Loading
    | Loaded (List Sermon)
    | LoadFailed


type UploadState
    = Idle
    | Uploading Float
    | UploadFailed String


type alias Model =
    { sermons : SermonList
    , upload : UploadState
    , confirmingDelete : Maybe Sermon
    , deleteError : Maybe String
    , zone : Time.Zone
    }


type Msg
    = GotZone Time.Zone
    | GotSermons (Result Http.Error (List Sermon))
    | FilePicked File
    | UploadProgress Http.Progress
    | UploadFinished (Result Http.Error ())
    | AskDelete Sermon
    | CancelDelete
    | ConfirmDelete Sermon
    | DeleteFinished (Result Http.Error ())
